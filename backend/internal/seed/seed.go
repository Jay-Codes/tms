// Package seed builds realistic tenant data straight against Postgres, using
// the same pure rules the HTTP handlers use — internal/contract.Generate for
// schedules, internal/payment.Allocate for money, internal/notify.Render for
// message bodies — so what a seeded org looks like is what a hand-driven org
// would look like.
//
// Two seeders live here:
//
//   - LoadTest builds the Phase 8 load-pass fixture (an org with properties,
//     units, renters, contracts, payments and notification rows) at whatever
//     size the caller asks for.
//   - Demo tops up the JJnE Rentals demo org so every FLOWS screen has
//     something on it.
//
// Both are idempotent: they look rows up by a natural key (property name, unit
// name within its property, renter phone) and create only what is missing.
// Reset deletes one org's data and nothing else.
package seed

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/config"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/expense"
	"tms/backend/internal/notify"
	"tms/backend/internal/validate"
)

// DateLayout matches the handlers' date format (API.md).
const DateLayout = "2006-01-02"

// Currency is the only currency in the MVP.
const Currency = "TZS"

// Contract and schedule statuses the seeder writes, spelled out rather than
// imported from httpserver (an internal package the seeder must not depend on).
const (
	contractActive           = "active"
	contractPendingSignature = "pending_signature"
	unitVacant               = "vacant"
	unitOccupied             = "occupied"
	kycSubmitted             = "submitted"
	partyRenter              = "renter"
	partyLandlord            = "landlord"
	methodOTPAccept          = "otp_accept"
	linkPending              = "pending"
)

// recommendedPeriods mirrors httpserver's org bootstrap: every org starts with
// the four preset payment periods, of which exactly one — Monthly — carries the
// "Recommended" badge (PLAN2 #8; migration 000012 enforces the one-per-org rule).
var recommendedPeriods = []struct {
	label       string
	days        int32
	recommended bool
}{
	{"Monthly", 30, true},
	{"Quarterly", 90, false},
	{"Half-year", 180, false},
	{"Yearly", 365, false},
}

// Seeder writes seed data against one database.
type Seeder struct {
	pool   *db.Pool
	q      *sqlc.Queries
	cfg    config.Config
	logger *slog.Logger
	now    time.Time
}

// New builds a Seeder. now is frozen for the whole run so "120 days ago" means
// the same thing in every row written.
func New(pool *db.Pool, cfg config.Config, logger *slog.Logger) *Seeder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Seeder{
		pool:   pool,
		q:      sqlc.New(pool),
		cfg:    cfg,
		logger: logger,
		now:    time.Now().UTC().Truncate(24 * time.Hour),
	}
}

// Summary is what a seeding run created or found.
type Summary struct {
	OrgName       string
	OrgID         string
	OwnerEmail    string
	Properties    int
	Units         int
	Renters       int
	Contracts     int
	Schedules     int
	Payments      int
	Reversed      int
	Notifications int
	LinkRequests  int
	UnitCodes     []UnitCode
	Notes         []string
}

// UnitCode is one unit's scan code, printed at the end of a run so the UAT
// script can link to it.
type UnitCode struct {
	Property string
	Unit     string
	Code     string
	Status   string
	Renter   string
}

func (s *Summary) note(format string, args ...any) {
	s.Notes = append(s.Notes, fmt.Sprintf(format, args...))
}

// ----------------------------------------------------------------- helpers --

func (s *Seeder) inTx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func isNoRows(err error) bool {
	return err != nil && strings.Contains(err.Error(), pgx.ErrNoRows.Error())
}

// Date wraps a time as a pgtype.Date at UTC midnight.
func Date(t time.Time) pgtype.Date {
	u := t.UTC()
	return pgtype.Date{Time: time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

// crockfordAlphabet is httpserver's unit-code alphabet (no I, L, O, U).
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

const unitCodeLen = 10

func randomUnitCode() (string, error) {
	buf := make([]byte, unitCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("seed: unit code: %w", err)
	}
	out := make([]byte, unitCodeLen)
	for i, b := range buf {
		out[i] = crockfordAlphabet[int(b)%len(crockfordAlphabet)]
	}
	return string(out), nil
}

func (s *Seeder) newUnitCode(ctx context.Context, q *sqlc.Queries) (string, error) {
	for i := 0; i < 5; i++ {
		code, err := randomUnitCode()
		if err != nil {
			return "", err
		}
		taken, err := q.UnitCodeTaken(ctx, code)
		if err != nil {
			return "", err
		}
		if !taken {
			return code, nil
		}
	}
	return "", fmt.Errorf("seed: no free unit code after 5 attempts")
}

// ------------------------------------------------------------ org bootstrap --

// orgBootstrap is the org an org-level seeder needs to hold on to.
type orgBootstrap struct {
	org     sqlc.Org
	owner   sqlc.User
	periods map[int32]sqlc.PaymentPeriod // by days
	tplID   pgtype.UUID
}

// ensureOrg finds the org by slug, or creates it with the same rows
// POST /orgs writes: owner user, membership, branding, default template and
// the four recommended payment periods.
func (s *Seeder) ensureOrg(ctx context.Context, name, slug, ownerName, ownerEmail, ownerPhone, password string, sum *Summary) (orgBootstrap, error) {
	var out orgBootstrap

	existing, err := s.q.GetOrgBySlug(ctx, slug)
	switch {
	case err == nil:
		out.org = existing
		sum.note("org %q already present (%s)", name, db.UUIDString(existing.ID))
	case isNoRows(err):
		settings := defaultSettings()
		raw, mErr := json.Marshal(settings)
		if mErr != nil {
			return out, mErr
		}
		hash, hErr := auth.HashSecret(password)
		if hErr != nil {
			return out, hErr
		}
		email := strings.ToLower(ownerEmail)
		txErr := s.inTx(ctx, func(q *sqlc.Queries) error {
			org, err := q.CreateOrg(ctx, sqlc.CreateOrgParams{Name: name, Slug: slug, Settings: raw})
			if err != nil {
				return err
			}
			owner, err := q.CreateUser(ctx, sqlc.CreateUserParams{
				Kind: auth.KindOrgUser, Phone: &ownerPhone, Email: &email,
				FullName: ownerName, PasswordHash: &hash,
				EmailVerifiedAt: db.TS(s.now),
			})
			if err != nil {
				return err
			}
			if _, err := q.CreateOrgMember(ctx, sqlc.CreateOrgMemberParams{
				OrgID: org.ID, UserID: owner.ID, Role: auth.RoleOwner,
			}); err != nil {
				return err
			}
			if _, err := q.CreateOrgBranding(ctx, sqlc.CreateOrgBrandingParams{
				OrgID: org.ID, DisplayName: name,
			}); err != nil {
				return err
			}
			swBody := contract.DefaultTemplateBodySW
			if _, err := q.CreateContractTemplate(ctx, sqlc.CreateContractTemplateParams{
				OrgID: org.ID, Name: contract.DefaultTemplateName,
				BodyHtml: contract.DefaultTemplateBody, BodyHtmlSw: &swBody, IsDefault: true,
			}); err != nil {
				return err
			}
			for i, p := range recommendedPeriods {
				if _, err := q.CreatePaymentPeriod(ctx, sqlc.CreatePaymentPeriodParams{
					OrgID: org.ID, Label: p.label, Days: p.days,
					IsRecommended: p.recommended, SortOrder: int32(i + 1),
				}); err != nil {
					return err
				}
			}
			if err := expense.SeedCategories(ctx, q, org.ID); err != nil {
				return err
			}
			out.org = org
			return audit.Record(ctx, q, audit.Entry{
				OrgID: db.UUIDString(org.ID), ActorUserID: db.UUIDString(owner.ID),
				Action: audit.ActionOrgCreate, EntityType: audit.EntityOrg,
				EntityID: db.UUIDString(org.ID),
				After:    map[string]any{"name": name, "slug": slug, "owner_email": email, "seeded": true},
			})
		})
		if txErr != nil {
			return out, fmt.Errorf("seed: create org: %w", txErr)
		}
		sum.note("org %q created (%s)", name, db.UUIDString(out.org.ID))
	default:
		return out, err
	}

	// Owner: by email, so a re-run against an existing org still resolves it.
	owner, err := s.q.GetUserByEmail(ctx, strings.ToLower(ownerEmail))
	if err != nil {
		return out, fmt.Errorf("seed: owner %s: %w", ownerEmail, err)
	}
	out.owner = owner

	periods, err := s.q.ListActivePaymentPeriods(ctx, out.org.ID)
	if err != nil {
		return out, err
	}
	out.periods = make(map[int32]sqlc.PaymentPeriod, len(periods))
	for _, p := range periods {
		if _, seen := out.periods[p.Days]; !seen {
			out.periods[p.Days] = p
		}
	}

	tpl, err := s.q.GetDefaultContractTemplate(ctx, out.org.ID)
	if err != nil {
		return out, fmt.Errorf("seed: default template: %w", err)
	}
	out.tplID = tpl.ID

	sum.OrgName = out.org.Name
	sum.OrgID = db.UUIDString(out.org.ID)
	sum.OwnerEmail = strings.ToLower(ownerEmail)
	return out, nil
}

// defaultSettings mirrors httpserver.DefaultOrgSettings; the seeder writes the
// JSON shape directly because that type is unexported to it.
func defaultSettings() map[string]any {
	return map[string]any{
		"auto_approve_links":     false,
		"due_day":                nil,
		"grace_days":             3,
		"reminder_offsets_days":  []int{7, 0},
		"unsigned_reminder_days": 7,
		"sms_language":           "sw",
	}
}

func settingsGraceDays(raw []byte) int32 {
	var s struct {
		GraceDays *int `json:"grace_days"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	if s.GraceDays == nil {
		return 3
	}
	return int32(*s.GraceDays)
}

func settingsLang(raw []byte) string {
	var s struct {
		SMSLanguage string `json:"sms_language"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	if s.SMSLanguage == "" {
		return "sw"
	}
	return s.SMSLanguage
}

// --------------------------------------------------------------- properties --

func (s *Seeder) ensureProperty(ctx context.Context, orgID pgtype.UUID, actor pgtype.UUID, name, location string) (sqlc.Property, bool, error) {
	rows, err := s.q.ListProperties(ctx, sqlc.ListPropertiesParams{OrgID: orgID, RowLimit: 200})
	if err != nil {
		return sqlc.Property{}, false, err
	}
	for _, r := range rows {
		if r.Name == name {
			return sqlc.Property{
				ID: r.ID, OrgID: orgID, Name: r.Name, LocationText: r.LocationText,
			}, false, nil
		}
	}
	var created sqlc.Property
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		var err error
		created, err = q.CreateProperty(ctx, sqlc.CreatePropertyParams{
			OrgID: orgID, Name: name, LocationText: location,
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionPropertyCreate, EntityType: audit.EntityProperty,
			EntityID: db.UUIDString(created.ID),
			After:    map[string]any{"name": name, "location_text": location, "seeded": true},
		})
	})
	return created, true, err
}

// -------------------------------------------------------------------- units --

type seededUnit struct {
	unit       sqlc.Unit
	property   sqlc.Property
	price      int64
	periodDays int32
	created    bool
}

// ensureUnit finds a unit by name inside its property, or creates it with an
// opening price plan.
func (s *Seeder) ensureUnit(ctx context.Context, orgID, actor pgtype.UUID, prop sqlc.Property, name string, price int64, periodDays int32) (seededUnit, error) {
	rows, err := s.q.ListUnits(ctx, sqlc.ListUnitsParams{
		OrgID: orgID, PropertyID: prop.ID, RowLimit: 500,
	})
	if err != nil {
		return seededUnit{}, err
	}
	for _, r := range rows {
		if r.Name == name {
			return seededUnit{
				unit: sqlc.Unit{
					ID: r.ID, OrgID: orgID, PropertyID: prop.ID, Name: r.Name,
					UnitCode: r.UnitCode, Status: r.Status,
				},
				property: prop, price: r.PriceAmount, periodDays: r.PricePeriodDays,
			}, nil
		}
	}

	out := seededUnit{property: prop, price: price, periodDays: periodDays, created: true}
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		code, err := s.newUnitCode(ctx, q)
		if err != nil {
			return err
		}
		unit, err := q.CreateUnit(ctx, sqlc.CreateUnitParams{
			OrgID: orgID, PropertyID: prop.ID, Name: name, UnitCode: code,
		})
		if err != nil {
			return err
		}
		out.unit = unit
		if _, err := q.CreatePricePlan(ctx, sqlc.CreatePricePlanParams{
			OrgID: orgID, UnitID: unit.ID, Amount: price, Currency: Currency,
			PeriodDays: periodDays, EffectiveFrom: Date(s.now.AddDate(0, 0, -180)),
			CreatedByUserID: actor,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionUnitCreate, EntityType: audit.EntityUnit,
			EntityID: db.UUIDString(unit.ID),
			After: map[string]any{
				"name": name, "unit_code": unit.UnitCode, "property_id": db.UUIDString(prop.ID),
				"price": price, "period_days": periodDays, "seeded": true,
			},
		})
	})
	return out, err
}

// ------------------------------------------------------------------ renters --

// ensureRenter finds a renter by phone, or registers one with a PIN and a
// submitted KYC profile (NIDA encrypted through the same pgcrypto path
// PUT /me/profile uses).
//
// orgID is the org whose seed run created the renter. A renter is a
// platform-level account, so this is not a tenancy — it is the only trace that
// says which seed run is responsible for the row, and Reset uses it to take a
// seeded renter away with the org that made them.
func (s *Seeder) ensureRenter(ctx context.Context, orgID pgtype.UUID, phone, fullName, pin, nida, kinName, kinPhone string) (sqlc.User, bool, error) {
	normalized, err := validate.NormalizePhone(phone)
	if err != nil {
		return sqlc.User{}, false, fmt.Errorf("seed: renter phone %q: %w", phone, err)
	}
	existing, err := s.q.GetUserByPhone(ctx, &normalized)
	if err == nil {
		return existing, false, nil
	}
	if !isNoRows(err) {
		return sqlc.User{}, false, err
	}

	pinHash, err := auth.HashSecret(pin)
	if err != nil {
		return sqlc.User{}, false, err
	}
	var created sqlc.User
	txErr := s.inTx(ctx, func(q *sqlc.Queries) error {
		user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
			Kind: auth.KindRenter, Phone: &normalized, FullName: fullName, PinHash: &pinHash,
		})
		if err != nil {
			return err
		}
		created = user
		if _, err := q.EnsureRenterProfile(ctx, sqlc.EnsureRenterProfileParams{
			UserID: user.ID, FullName: fullName,
		}); err != nil {
			return err
		}
		if _, err := q.UpsertRenterProfile(ctx, sqlc.UpsertRenterProfileParams{
			UserID: user.ID, FullName: fullName,
			NidaNumber: &nida, EncKey: s.cfg.NidaEncKey,
			NextOfKinName: &kinName, NextOfKinPhone: &kinPhone,
		}); err != nil {
			return err
		}
		if _, err := q.SetRenterKycStatus(ctx, sqlc.SetRenterKycStatusParams{
			KycStatus: kycSubmitted, UserID: user.ID,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID:       db.UUIDString(orgID),
			ActorUserID: db.UUIDString(user.ID), Action: audit.ActionRegisterRenter,
			EntityType: audit.EntityUser, EntityID: db.UUIDString(user.ID),
			After: map[string]any{"phone": normalized, "seeded": true},
		})
	})
	return created, true, txErr
}

// ---------------------------------------------------------------- contracts --

// contractSpec is one tenancy the seeder wants to exist.
type contractSpec struct {
	orgID      pgtype.UUID
	orgName    string
	actor      pgtype.UUID
	templateID pgtype.UUID
	unit       seededUnit
	renter     sqlc.User
	period     sqlc.PaymentPeriod
	termDays   int32
	start      time.Time
	// status is `active` (signed, countersigned, schedules generated) or
	// `pending_signature` (waiting on the renter).
	status string
}

type contractResult struct {
	contractID pgtype.UUID
	schedules  []sqlc.PaymentSchedule
	created    bool
}

// ensureContract creates the tenancy the spec describes, taking the same steps
// the approve → sign → activate path takes: terms rendered and snapshotted,
// hash computed, signatures recorded, schedules generated by
// contract.Generate, unit flipped to occupied.
//
// It is a no-op when the unit already carries a live contract.
func (s *Seeder) ensureContract(ctx context.Context, spec contractSpec) (contractResult, error) {
	var out contractResult

	live, err := s.q.CountLiveContractsForUnit(ctx, sqlc.CountLiveContractsForUnitParams{
		OrgID: spec.orgID, UnitID: spec.unit.unit.ID,
	})
	if err != nil {
		return out, err
	}
	if live > 0 {
		return out, nil
	}

	start := spec.start.UTC().Truncate(24 * time.Hour)
	end := contract.EndDate(start, int(spec.termDays))
	terms := contract.Render(contract.SanitizeHTML(contract.DefaultTemplateBody), map[string]string{
		"renter_name": spec.renter.FullName,
		"unit":        spec.unit.unit.Name,
		"property":    spec.unit.property.Name,
		// The document states what falls due each payment period, with the
		// unit's own price beside it (PLAN2 Phase 9).
		"rent": notify.FormatTZS(contract.RentPerPeriod(
			spec.unit.price, int(spec.unit.periodDays), int(spec.period.Days))),
		"rent_basis": contract.RentBasisPhrase(
			notify.FormatTZS(spec.unit.price), int(spec.unit.periodDays)),
		"start_date":     start.Format(DateLayout),
		"end_date":       end.Format(DateLayout),
		"payment_period": fmt.Sprintf("%s (%d days)", spec.period.Label, spec.period.Days),
		"org_name":       spec.orgName,
		"term_days":      strconv.Itoa(int(spec.termDays)),
		"due_day":        contract.DueDayPhrase(nil),
	})
	hash := contract.Snapshot{
		TermsHTML: terms, UnitID: db.UUIDString(spec.unit.unit.ID),
		RenterUserID: db.UUIDString(spec.renter.ID), RentAmount: spec.unit.price,
		RentPeriodDays: int(spec.unit.periodDays), PaymentPeriodDays: int(spec.period.Days),
		TermDays: int(spec.termDays), StartDate: start.Format(DateLayout),
		EndDate: end.Format(DateLayout),
	}.Hash()

	rows := contract.Generate(int(spec.unit.price), int(spec.unit.periodDays),
		int(spec.termDays), int(spec.period.Days), start, nil)

	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		created, err := q.CreateContract(ctx, sqlc.CreateContractParams{
			OrgID: spec.orgID, UnitID: spec.unit.unit.ID, RenterUserID: spec.renter.ID,
			TemplateID: spec.templateID, TermsSnapshotHtml: terms,
			RentAmount: spec.unit.price, RentPeriodDays: spec.unit.periodDays,
			PaymentPeriodID: spec.period.ID, PaymentPeriodDays: spec.period.Days,
			TermDays:  spec.termDays,
			StartDate: Date(start), EndDate: Date(end),
			Status: contractPendingSignature, SnapshotHash: &hash,
		})
		if err != nil {
			return err
		}
		out.contractID = created.ID
		out.created = true

		if err := audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(spec.orgID), ActorUserID: db.UUIDString(spec.actor),
			Action: audit.ActionContractCreate, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(created.ID),
			After: map[string]any{
				"unit_id": db.UUIDString(spec.unit.unit.ID), "renter_user_id": db.UUIDString(spec.renter.ID),
				"rent_amount": spec.unit.price, "term_days": spec.termDays,
				"payment_period_days": spec.period.Days, "start_date": start.Format(DateLayout),
				"status": contractPendingSignature, "snapshot_hash": hash, "seeded": true,
			},
		}); err != nil {
			return err
		}
		if spec.status == contractPendingSignature {
			return nil
		}

		// Signed by the renter (OTP), countersigned by the landlord — the two
		// append-only rows POST /sign and POST /activate write.
		if _, err := q.CreateContractSignature(ctx, sqlc.CreateContractSignatureParams{
			OrgID: spec.orgID, ContractID: created.ID, Party: partyRenter,
			UserID: spec.renter.ID, Method: methodOTPAccept, SnapshotHash: hash,
		}); err != nil {
			return err
		}
		if _, err := q.CreateContractSignature(ctx, sqlc.CreateContractSignatureParams{
			OrgID: spec.orgID, ContractID: created.ID, Party: partyLandlord,
			UserID: spec.actor, Method: methodOTPAccept, SnapshotHash: hash,
		}); err != nil {
			return err
		}
		if _, err := q.ActivateContract(ctx, sqlc.ActivateContractParams{
			OrgID: spec.orgID, ID: created.ID,
		}); err != nil {
			return err
		}
		for _, gen := range rows {
			sched, err := q.CreatePaymentSchedule(ctx, sqlc.CreatePaymentScheduleParams{
				OrgID: spec.orgID, ContractID: created.ID,
				PeriodStart: Date(gen.PeriodStart), PeriodEnd: Date(gen.PeriodEnd),
				DueDate: Date(gen.DueDate), Amount: gen.Amount,
			})
			if err != nil {
				return err
			}
			out.schedules = append(out.schedules, sched)
		}
		if _, err := q.SetUnitStatusDerived(ctx, sqlc.SetUnitStatusDerivedParams{
			Status: unitOccupied, OrgID: spec.orgID, ID: spec.unit.unit.ID,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(spec.orgID), ActorUserID: db.UUIDString(spec.actor),
			Action: audit.ActionContractActivate, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(created.ID),
			Before:   map[string]any{"status": contractPendingSignature},
			After: map[string]any{
				"status": contractActive, "schedules": len(rows),
				"unit_status": unitOccupied, "seeded": true,
			},
		})
	})
	return out, err
}

// ----------------------------------------------------------------- payments --

// notifyRow appends one notification_log row in the state the worker leaves a
// delivered message in. The dedupe key is the same shape the scheduler uses,
// so a re-run collides and inserts nothing (ON CONFLICT DO NOTHING).
func (s *Seeder) notifyRow(ctx context.Context, q *sqlc.Queries, orgID, userID pgtype.UUID, kind, lang, phone, dedupe string, vars notify.Vars, sentAt time.Time) (bool, error) {
	body := notify.Render(kind, lang, vars, nil)
	if body == "" {
		return false, nil
	}
	payload, err := json.Marshal(map[string]any{"seeded": true, "kind": kind})
	if err != nil {
		return false, err
	}
	row, err := q.InsertNotification(ctx, sqlc.InsertNotificationParams{
		OrgID: orgID, UserID: userID, Kind: kind, DedupeKey: dedupe,
		Payload: payload, ToPhone: phone, Body: body,
		Language: notify.LanguageFor(lang, ""),
	})
	if isNoRows(err) {
		return false, nil // dedupe hit: already seeded
	}
	if err != nil {
		return false, err
	}
	if err := q.MarkNotificationSent(ctx, sqlc.MarkNotificationSentParams{
		ID: row.ID, Attempts: 1, ProviderMsgID: db.Str("seed-" + db.UUIDString(row.ID)[:8]),
	}); err != nil {
		return false, err
	}
	_ = sentAt
	return true, nil
}
