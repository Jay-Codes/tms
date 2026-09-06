package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/theme"
)

// Seed v2 — the Part 2 fixture (PLAN2 Phase 15).
//
// Phase 8's seeder built an org that exercised the *shapes* Part 1 reads:
// units, contracts, schedules, a handful of payments. Part 2 reads time. A
// revenue chart over four months of data is a chart with four points on it,
// which is neither a useful demo nor a useful load test — the queries that
// matter are the ones that scan a year and bucket it.
//
// So this file adds the twelve months: expenses per property per month,
// payments spread across the year (by starting the tenancies across it), SMS
// credit with a ledger to explain it, and the two Part 2 rows an admin screen
// has nothing to show without — a held message and an edited platform
// template.
//
// Everything here is idempotent on a natural key, like the rest of the seeder:
// an expense by (property, date, amount), credits by the balance already on
// the org, a theme by the row already stored.

// ExpenseMonths is how far back the expense ledger reaches. Twelve months is
// what the Reports cadences need: `year` shows a full circle, and
// `half_year`/`quarter` each land on a window with data on both sides of it.
const ExpenseMonths = 12

// expenseKind is one recurring cost, tied to the category it is filed under.
// The names are the eight defaults expense.SeedCategories writes, so a seeded
// expense is filed exactly as a landlord would file it.
type expenseKind struct {
	category string
	vendors  []string
	// min/max bound the amount in TZS. The ranges are the ones a Dar es
	// Salaam block actually runs at, so the report totals read as money.
	min, max int64
	// everyMonth marks a cost that recurs monthly (utilities, security);
	// the rest arrive when they arrive.
	everyMonth bool
	notes      []string
}

//nolint:gochecknoglobals // a fixed table, read-only.
var expenseKinds = []expenseKind{
	{
		category: "Utilities", everyMonth: true,
		vendors: []string{"TANESCO", "DAWASA", "Umeme Prepaid"},
		min:     85_000, max: 420_000,
		notes: []string{"Umeme wa mwezi", "Maji ya mwezi", "Bili ya umeme — mita ya jumla"},
	},
	{
		category: "Security", everyMonth: true,
		vendors: []string{"Ultimate Security Ltd", "SGA Tanzania", "Knight Support"},
		min:     150_000, max: 380_000,
		notes: []string{"Walinzi wa usiku", "Mkataba wa ulinzi"},
	},
	{
		category: "Cleaning", everyMonth: true,
		vendors: []string{"Safi Cleaners", "Mama Neema Cleaning"},
		min:     60_000, max: 180_000,
		notes: []string{"Usafi wa maeneo ya pamoja"},
	},
	{
		category: "Repairs & maintenance",
		vendors:  []string{"Mwenge Hardware", "Fundi Juma", "Kariakoo Plumbing", "Msasani Electricals"},
		min:      45_000, max: 950_000,
		notes: []string{
			"Kubadilisha bomba la maji", "Rangi ya ukuta wa nje",
			"Kuziba paa linalovuja", "Kubadili swichi za umeme",
		},
	},
	{
		category: "Taxes & levies",
		vendors:  []string{"TRA", "Kinondoni Municipal Council", "Temeke Municipal Council"},
		min:      120_000, max: 1_400_000,
		notes: []string{"Kodi ya majengo", "Ada ya halmashauri"},
	},
	{
		category: "Insurance",
		vendors:  []string{"Jubilee Insurance", "Alliance Insurance"},
		min:      300_000, max: 900_000,
		notes: []string{"Bima ya jengo — awamu"},
	},
	{
		category: "Management fees",
		vendors:  []string{"", "Mkasi Property Services"},
		min:      80_000, max: 260_000,
		notes: []string{"Ada ya usimamizi"},
	},
	{
		category: "Other",
		vendors:  []string{"", "Duka la Mbezi"},
		min:      20_000, max: 140_000,
		notes: []string{"Vifaa vya ofisi", "Gharama ndogondogo"},
	},
}

// voidReasons are the corrections a real ledger carries: an expense recorded
// twice, or against the wrong block. A void is a record, not a deletion, so
// each one needs a reason a landlord would recognise months later.
//
//nolint:gochecknoglobals // fixed list, read-only.
var voidReasons = []string{
	"Imeingizwa mara mbili — tazama risiti ya awali",
	"Iliingizwa kwenye jengo lisilo sahihi",
	"Kiasi kilikuwa si sahihi; imeandikwa upya",
}

// expenseKey is the natural key a re-run recognises: one property does not
// spend exactly the same amount on exactly the same day twice, and if it did,
// seeding it a second time is the wrong answer anyway.
func expenseKey(propertyID pgtype.UUID, on time.Time, amount int64) string {
	return fmt.Sprintf("%s|%s|%d", db.UUIDString(propertyID), on.Format(DateLayout), amount)
}

// ensureExpenses writes ExpenseMonths of ledger for every property.
//
// Each month gets between two and six expenses: the three monthly costs
// (utilities, security, cleaning) plus a draw from the occasional ones. About
// one in ten is voided, so the ledger, the summary and the revenue series all
// have to agree on excluding it — which is the arithmetic Phase 11's
// reconciliation tests pin.
//
// The generator is seeded from the property id, so a re-run produces the same
// ledger rather than a second, different one beside it.
func (s *Seeder) ensureExpenses(ctx context.Context, orgID, actor pgtype.UUID,
	props []sqlc.Property, sum *Summary,
) error {
	if len(props) == 0 {
		return nil
	}

	cats, err := s.q.ListExpenseCategories(ctx, orgID)
	if err != nil {
		return err
	}
	catByName := make(map[string]pgtype.UUID, len(cats))
	for _, c := range cats {
		catByName[c.Name] = c.ID
	}

	// Everything already on the ledger, so a re-run adds nothing.
	existing := map[string]bool{}
	rows, err := s.q.ListExpenses(ctx, sqlc.ListExpensesParams{OrgID: orgID, RowLimit: 5000})
	if err != nil {
		return err
	}
	for _, r := range rows {
		existing[expenseKey(r.PropertyID, r.IncurredOn.Time, r.Amount)] = true
	}

	created, voided := 0, 0
	for _, prop := range props {
		// A per-property stream: the same property gets the same ledger on
		// every run, and two properties never get the same one.
		rng := rand.New(rand.NewSource(int64(hashUUID(prop.ID))))

		for m := ExpenseMonths; m >= 1; m-- {
			// The month window, walking back from the current month.
			monthStart := time.Date(s.now.Year(), s.now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -(m - 1), 0)

			picks := monthlyKinds(rng)
			for _, k := range picks {
				day := 1 + rng.Intn(27)
				on := monthStart.AddDate(0, 0, day-1)
				if on.After(s.now) {
					continue // the current month is only as long as it is
				}
				// Round to the nearest 500 TZS: nobody writes an invoice for
				// 137 419 shillings, and a chart of round numbers reads as
				// money rather than as noise.
				amount := (k.min + rng.Int63n(k.max-k.min+1)) / 500 * 500

				vendor := k.vendors[rng.Intn(len(k.vendors))]
				reference := ""
				// Roughly three in five carry a document number; the rest are
				// the ones recorded from a photo of a till slip.
				if vendor != "" && rng.Intn(5) < 3 {
					reference = fmt.Sprintf("INV-%s-%04d", on.Format("200601"), 1+rng.Intn(9000))
				}
				note := ""
				if len(k.notes) > 0 {
					note = k.notes[rng.Intn(len(k.notes))]
				}
				// About one in ten is a correction. The reason is drawn
				// whether or not the row is voided, so the generator consumes
				// the same number of values either way.
				void := rng.Intn(10) == 0
				voidReason := voidReasons[rng.Intn(len(voidReasons))]

				// The skip comes *after* every draw, not before: bailing out
				// early would leave the generator at a different point on a
				// re-run, and the run would write a second, differently-shaped
				// ledger beside the first instead of recognising it.
				if existing[expenseKey(prop.ID, on, amount)] {
					continue
				}

				if err := s.writeExpense(ctx, orgID, actor, prop, catByName[k.category], expenseRow{
					amount: amount, on: on, vendor: vendor, reference: reference,
					note: note, void: void, voidReason: voidReason,
				}); err != nil {
					return fmt.Errorf("seed: expense on %q: %w", prop.Name, err)
				}
				existing[expenseKey(prop.ID, on, amount)] = true
				created++
				if void {
					voided++
				}
			}
		}
	}

	sum.Expenses += created
	sum.ExpensesVoided += voided
	if created > 0 {
		sum.note("%d expense(s) written across %d propert(ies) and %d months (%d voided)",
			created, len(props), ExpenseMonths, voided)
	}
	return nil
}

// monthlyKinds picks one month's costs: the three recurring ones always, plus
// zero to three of the occasional ones — two to six expenses in the month,
// which is what a small block actually generates.
func monthlyKinds(rng *rand.Rand) []expenseKind {
	var out []expenseKind
	var occasional []expenseKind
	for _, k := range expenseKinds {
		if k.everyMonth {
			out = append(out, k)
			continue
		}
		occasional = append(occasional, k)
	}
	// Two of the three recurring costs at minimum: not every landlord pays for
	// cleaning every month, and a ledger where every month is identical hides
	// the bugs a real one would surface.
	if rng.Intn(4) == 0 && len(out) > 0 {
		out = out[:len(out)-1]
	}
	extra := rng.Intn(4)
	rng.Shuffle(len(occasional), func(i, j int) { occasional[i], occasional[j] = occasional[j], occasional[i] })
	for i := 0; i < extra && i < len(occasional); i++ {
		out = append(out, occasional[i])
	}
	return out
}

// expenseRow is one generated expense, before it becomes a database row.
type expenseRow struct {
	amount     int64
	on         time.Time
	vendor     string
	reference  string
	note       string
	void       bool
	voidReason string
}

// writeExpense records one expense (and, if the generator said so, the void
// that corrects it) with the same audit rows POST /expenses and
// POST /expenses/{id}/void write.
func (s *Seeder) writeExpense(ctx context.Context, orgID, actor pgtype.UUID,
	prop sqlc.Property, categoryID pgtype.UUID, row expenseRow,
) error {
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		created, err := q.CreateExpense(ctx, sqlc.CreateExpenseParams{
			OrgID: orgID, PropertyID: prop.ID, CategoryID: categoryID,
			Amount: row.amount, IncurredOn: Date(row.on),
			Vendor: row.vendor, Reference: row.reference, Note: row.note,
			RecordedByUserID: actor,
		})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionExpenseCreate, EntityType: audit.EntityExpense,
			EntityID: db.UUIDString(created.ID),
			After: map[string]any{
				"property_id": db.UUIDString(prop.ID), "amount": row.amount,
				"incurred_on": row.on.Format(DateLayout), "vendor": row.vendor,
				"seeded": true,
			},
		}); err != nil {
			return err
		}
		if !row.void {
			return nil
		}
		reason := row.voidReason
		voidedRow, err := q.VoidExpense(ctx, sqlc.VoidExpenseParams{
			VoidReason: &reason, OrgID: orgID, ID: created.ID,
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionExpenseVoid, EntityType: audit.EntityExpense,
			EntityID: db.UUIDString(created.ID),
			Before:   map[string]any{"status": "recorded"},
			After:    map[string]any{"status": voidedRow.Status, "void_reason": reason, "seeded": true},
		})
	})
}

// hashUUID turns a uuid into a stable seed for math/rand. It is not a hash in
// any cryptographic sense and does not need to be: all it has to do is give
// two different properties two different ledgers, reproducibly.
func hashUUID(id pgtype.UUID) uint64 {
	var out uint64 = 1469598103934665603 // FNV-1a offset basis
	for _, b := range id.Bytes {
		out ^= uint64(b)
		out *= 1099511628211
	}
	return out >> 1 // keep it positive when it lands in an int64
}

// ------------------------------------------------------------ SMS credits --

// SeededCredits is the balance `make seed` leaves on a seeded org — enough for
// a couple of bulk sends plus a month of reminders, so the notification screens
// have a number that is neither zero nor infinite.
const SeededCredits = 500

// ensureSMSCredits tops an org up to `target` and writes the ledger row that
// explains where the credit came from, the way POST /admin/orgs/{id}/sms/topup
// does. A re-run that finds the balance already at or above target does
// nothing: the ledger is append-only, and a seeder that appends on every run
// is a seeder that invents a credit history.
func (s *Seeder) ensureSMSCredits(ctx context.Context, orgID, adminUserID pgtype.UUID,
	target int32, sum *Summary,
) error {
	current, err := s.q.EnsureOrgSMSCredits(ctx, orgID)
	if err != nil {
		return err
	}
	if current.Balance >= target {
		sum.Credits = int(current.Balance)
		return nil
	}
	delta := target - current.Balance

	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		balance, err := q.AddOrgSMSCredits(ctx, sqlc.AddOrgSMSCreditsParams{
			Delta: delta, OrgID: orgID,
		})
		if err != nil {
			return err
		}
		if _, err := q.InsertSMSCreditLedger(ctx, sqlc.InsertSMSCreditLedgerParams{
			OrgID: orgID, Delta: delta, BalanceAfter: balance, Reason: "topup",
			AdminUserID: adminUserID, Note: "seeded opening balance",
		}); err != nil {
			return err
		}
		sum.Credits = int(balance)
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(adminUserID),
			Action: audit.ActionSMSCreditTopup, EntityType: audit.EntitySMSCredits,
			EntityID: db.UUIDString(orgID),
			Before:   map[string]any{"balance": current.Balance},
			After:    map[string]any{"balance": balance, "delta": delta, "seeded": true},
		})
	})
	if err != nil {
		return fmt.Errorf("seed: sms credits: %w", err)
	}
	sum.note("sms credits topped up to %d (ledger reason `topup`)", target)
	return nil
}

// ensureHeldMessages leaves a couple of `held_no_credit` rows on the org, so
// the admin credit screen and the landlord's low-credit warning have the state
// they are built to show. The rows are written queued and then held by the
// same statement the worker uses when a debit fails — they are never handed to
// the SMS provider, so nothing is sent.
func (s *Seeder) ensureHeldMessages(ctx context.Context, orgID pgtype.UUID,
	lang, orgName string, sum *Summary,
) error {
	held, err := s.q.CountHeldNotifications(ctx, orgID)
	if err != nil {
		return err
	}
	if held > 0 {
		return nil
	}

	rows, err := s.q.ListSchedules(ctx, sqlc.ListSchedulesParams{OrgID: orgID, RowLimit: 40})
	if err != nil {
		return err
	}
	want := 2
	made := 0
	for _, sc := range rows {
		if made >= want {
			break
		}
		u, err := s.q.GetUserByID(ctx, sc.RenterUserID)
		if err != nil {
			continue
		}
		phone := db.StrVal(u.Phone)
		if phone == "" {
			continue
		}
		dedupe := fmt.Sprintf("seed:held:%s", db.UUIDString(sc.ID))
		body := notify.Render(notify.KindReminderDue, notify.LanguageFor(u.Locale, lang), notify.Vars{
			Name: sc.RenterName, Amount: notify.FormatTZS(sc.Amount - sc.PaidAmount),
			DueDate: sc.DueDate.Time.Format(DateLayout), Unit: sc.UnitName,
			Property: sc.PropertyName, Org: orgName,
		}, nil)
		if body == "" {
			continue
		}
		payload, err := json.Marshal(map[string]any{"seeded": true, "kind": notify.KindReminderDue})
		if err != nil {
			return err
		}
		err = s.inTx(ctx, func(q *sqlc.Queries) error {
			row, err := q.InsertNotification(ctx, sqlc.InsertNotificationParams{
				OrgID: orgID, UserID: sc.RenterUserID, Kind: notify.KindReminderDue,
				DedupeKey: dedupe, Payload: payload, ToPhone: phone, Body: body,
				Language: notify.LanguageFor(u.Locale, lang),
			})
			if isNoRows(err) {
				return nil // already seeded
			}
			if err != nil {
				return err
			}
			made++
			return q.HoldNotificationNoCredit(ctx, row.ID)
		})
		if err != nil {
			return fmt.Errorf("seed: held message: %w", err)
		}
	}
	if made > 0 {
		sum.Held += made
		sum.note("%d message(s) left `held_no_credit` for the admin credit screen", made)
	}
	return nil
}

// ------------------------------------------------------------------ theme --

// DemoThemePreset is the preset the JJnE demo saves, so the branding screen
// opens on something other than the default and the tenant apps repaint.
const DemoThemePreset = "cool_slate"

// DemoThemeAccent is the one token the demo overrides. It is a deeper teal
// than the preset's own accent, chosen because it clears AA against both the
// preset's paper and its surface — the validator refuses anything that does
// not, and a seeder that writes a theme the API would reject is a seeder that
// puts the product in a state a user cannot reach.
const DemoThemeAccent = "#0f5c4a"

// isDefaultTheme reports whether a stored theme is still the untouched
// default: the shipped default preset, no per-token override.
func isDefaultTheme(t sqlc.OrgTheme) bool {
	if db.StrVal(t.PresetID) != theme.DefaultPresetID {
		return false
	}
	if len(t.Tokens) == 0 {
		return true
	}
	var tokens theme.Tokens
	if err := json.Unmarshal(t.Tokens, &tokens); err != nil {
		return false
	}
	return tokens == theme.Tokens{}
}

// ensureDemoTheme saves the demo org's theme: a shipped preset with one token
// moved, validated through the same theme.Validate the PUT calls. A row
// already there is left alone.
func (s *Seeder) ensureDemoTheme(ctx context.Context, orgID, actor pgtype.UUID, sum *Summary) error {
	// A row is not the same as a choice. An org that has opened the branding
	// screen and saved without changing anything carries the default preset
	// and no token override — the same state as never having looked — so the
	// demo treats that as "nothing to preserve" and writes its theme. Anything
	// else is somebody's actual pick and is left alone.
	switch current, err := s.q.GetOrgTheme(ctx, orgID); {
	case err == nil && !isDefaultTheme(current):
		sum.note("theme already chosen (%s) — left untouched", db.StrVal(current.PresetID))
		return nil
	case err != nil && !isNoRows(err):
		return err
	}

	preset, ok := theme.PresetByID(DemoThemePreset)
	if !ok {
		return fmt.Errorf("seed: no theme preset %q", DemoThemePreset)
	}
	tokens := preset.Tokens
	tokens.Accent = DemoThemeAccent
	tokens = theme.NormalizeTokens(tokens)

	// The same gate PUT /org/branding puts a submitted theme through. A
	// failure here is a bug in this file, not in the caller's input, so it
	// stops the run rather than being warned about.
	if fails := theme.Validate(tokens, preset.FontID); len(fails) > 0 {
		return fmt.Errorf("seed: demo theme fails contrast validation: %+v", fails)
	}

	raw, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	presetID := preset.ID
	fontID := preset.FontID
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.UpsertOrgTheme(ctx, sqlc.UpsertOrgThemeParams{
			OrgID: orgID, PresetID: &presetID, Tokens: raw, FontID: &fontID,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionBrandingThemeUpdate, EntityType: audit.EntityOrg,
			EntityID: db.UUIDString(orgID),
			After: map[string]any{
				"preset_id": presetID, "font_id": fontID,
				"accent": tokens.Accent, "seeded": true,
			},
		})
	})
	if err != nil {
		return fmt.Errorf("seed: demo theme: %w", err)
	}
	sum.note("theme saved: %s preset with accent %s", presetID, tokens.Accent)
	return nil
}

// -------------------------------------------------- platform template edit --

// DemoEditedTemplateKind is the kind `seed-demo` gives an edit history to, so
// the admin template screen has a version list with something in it.
const DemoEditedTemplateKind = notify.KindReminder7d

// ensureTemplateHistory edits one platform template's English wording and then
// puts the original back, leaving two rows in the version table: the wording
// before the edit, and the edit itself before the revert.
//
// Both steps go through the same queries the admin handlers use, so the
// history reads exactly as it would if a person had done it — which is the
// point: the admin screen is being demonstrated, not simulated.
//
// A kind that already has versions is left alone; the history is a fixture,
// and appending to it on every run would grow it without bound.
func (s *Seeder) ensureTemplateHistory(ctx context.Context, adminUserID pgtype.UUID, sum *Summary) error {
	kind := DemoEditedTemplateKind

	versions, err := s.q.ListPlatformTemplateVersions(ctx, kind)
	if err != nil {
		return err
	}
	if len(versions) > 0 {
		sum.note("platform template %q already has %d version(s) — left untouched", kind, len(versions))
		return nil
	}

	original, err := s.q.GetPlatformTemplate(ctx, kind)
	if isNoRows(err) {
		sum.note("platform template %q is not in the catalogue — skipped", kind)
		return nil
	}
	if err != nil {
		return err
	}

	// The edit: the same message, said a little more plainly. It has to carry
	// exactly the variables the kind declares — the admin PUT validates that,
	// and a seeded row that would not pass validation is a lie about the
	// product.
	edited := original.En + " Reply STOP to opt out."

	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		// Step one: the edit. The version row carries the wording being
		// replaced, so history reads "version N said this".
		if err := q.InsertPlatformTemplateVersion(ctx, sqlc.InsertPlatformTemplateVersionParams{
			Kind: kind, Version: original.Version, Sw: original.Sw, En: original.En,
			AdminUserID: adminUserID,
		}); err != nil {
			return err
		}
		afterEdit, err := q.UpdatePlatformTemplate(ctx, sqlc.UpdatePlatformTemplateParams{
			Kind: kind, Sw: original.Sw, En: edited, AdminUserID: adminUserID,
		})
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, q, audit.Entry{
			ActorUserID: db.UUIDString(adminUserID),
			Action:      audit.ActionPlatformTemplateUpdate, EntityType: audit.EntityPlatformTemplate,
			EntityID: kind,
			Before:   map[string]any{"en": original.En, "version": original.Version},
			After:    map[string]any{"en": edited, "version": afterEdit.Version, "seeded": true},
		}); err != nil {
			return err
		}

		// Step two: the revert, which is itself an edit — the wording being
		// replaced (the one from step one) is versioned before the original
		// goes back. Two rows in the history, which is what the screen shows.
		if err := q.InsertPlatformTemplateVersion(ctx, sqlc.InsertPlatformTemplateVersionParams{
			Kind: kind, Version: afterEdit.Version, Sw: afterEdit.Sw, En: afterEdit.En,
			AdminUserID: adminUserID,
		}); err != nil {
			return err
		}
		afterRevert, err := q.UpdatePlatformTemplate(ctx, sqlc.UpdatePlatformTemplateParams{
			Kind: kind, Sw: original.Sw, En: original.En, AdminUserID: adminUserID,
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			ActorUserID: db.UUIDString(adminUserID),
			Action:      audit.ActionPlatformTemplateRevert, EntityType: audit.EntityPlatformTemplate,
			EntityID: kind,
			Before:   map[string]any{"en": edited, "version": afterEdit.Version},
			After: map[string]any{
				"en": original.En, "version": afterRevert.Version,
				"reverted_to": original.Version, "seeded": true,
			},
		})
	})
	if err != nil {
		return fmt.Errorf("seed: platform template history: %w", err)
	}
	sum.note("platform template %q edited and reverted — 2 versions in its history", kind)
	return nil
}

// platformAdminID resolves the platform admin to attribute a seeded top-up or
// template edit to. There may not be one (the admin is seeded by cmd/api on
// boot, from ADMIN_EMAIL), in which case the caller gets an invalid uuid and
// writes a row with a null actor — which is honest: nobody did it.
func (s *Seeder) platformAdminID(ctx context.Context) pgtype.UUID {
	var id pgtype.UUID
	if err := s.pool.QueryRow(ctx,
		`SELECT id FROM users WHERE kind = 'platform_admin' AND deleted_at IS NULL
		 ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		return pgtype.UUID{}
	}
	return id
}
