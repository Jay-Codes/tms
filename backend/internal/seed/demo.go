package seed

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/payment"
)

// DemoSlug is the demo org `make seed-demo` tops up.
const DemoSlug = "jjne-rentals"

// demoRenter is one renter the demo needs, with the state their tenancy is in.
type demoRenter struct {
	phone    string
	name     string
	nida     string
	kinName  string
	kinPhone string
	// property/unit name the tenancy sits on. The unit is created if absent.
	property string
	unit     string
	price    int64
	cadence  int32
	termDays int32
	// startOffsetDays is how far back the tenancy starts, in days. A tenancy
	// that started longer ago than its cadence has a schedule already due.
	startOffsetDays int
	// state is what the seeder leaves behind: an active tenancy, an unsigned
	// contract, or a link request nobody has decided yet.
	state string
	// payFirst settles the first schedule, so the demo has a thank-you SMS and
	// a `paid` chip as well as an overdue one.
	payFirst bool
}

const (
	demoStateActive   = "active"
	demoStateUnsigned = "pending_signature"
	demoStateLink     = "link_request"
)

// demoRenters is the JJnE top-up. Asha (+255755000111) is deliberately absent:
// her tenancy is hand-made demo data and the seeder never touches it.
var demoRenters = []demoRenter{
	{
		phone: "+255766000001", name: "Neema Kileo", nida: "19880412000000101",
		kinName: "Joseph Kileo", kinPhone: "+255766100001",
		property: "Mbezi Beach Block A", unit: "Room 4", price: 250_000,
		cadence: 30, termDays: 360, startOffsetDays: 45,
		state: demoStateActive, // first schedule due 45 days ago → overdue
	},
	{
		phone: "+255766000002", name: "Baraka Mushi", nida: "19910822000000102",
		kinName: "Rose Mushi", kinPhone: "+255766100002",
		property: "Mbezi Beach Block A", unit: "Room 5", price: 300_000,
		cadence: 30, termDays: 360, startOffsetDays: 40,
		state: demoStateActive, payFirst: true, // paid → thank-you SMS
	},
	{
		phone: "+255766000003", name: "Upendo Sanga", nida: "19950203000000103",
		kinName: "Frank Sanga", kinPhone: "+255766100003",
		property: "Kigamboni Court", unit: "Room 4", price: 180_000,
		cadence: 90, termDays: 360, startOffsetDays: 0,
		state: demoStateActive, // starts today → everything pending
	},
	{
		phone: "+255766000004", name: "Hamisi Ngassa", nida: "19930517000000104",
		kinName: "Zainabu Ngassa", kinPhone: "+255766100004",
		property: "Kigamboni Court", unit: "Shop B", price: 450_000,
		cadence: 30, termDays: 180, startOffsetDays: -7,
		state: demoStateUnsigned, // waiting on the renter's signature
	},
	{
		phone: "+255766000005", name: "Cecilia Mrema", nida: "19970930000000105",
		kinName: "Peter Mrema", kinPhone: "+255766100005",
		property: "Mbezi Beach Block A", unit: "Room 1", price: 0,
		cadence: 30, termDays: 180, startOffsetDays: -14,
		state: demoStateLink, // pending link request in the landlord's inbox
	},
}

// demoBankAccount is the collection account the renter payment screen shows.
var demoBankAccount = map[string]any{
	"bank_name":      "CRDB Bank",
	"account_name":   "JJnE Rentals Ltd",
	"account_number": "0150412345600",
	"instructions":   "Use your unit name as the payment reference, e.g. \"Room 4 — Mbezi\".",
}

// Demo tops the JJnE Rentals org up so every FLOWS screen has data on it: a
// bank account, English notification settings, three more tenancies (one
// overdue, one paid, one brand new), one contract waiting for a signature and
// one link request waiting for a decision.
//
// It creates only what is missing, keyed on renter phone and unit name, and
// never edits a row it did not create.
func (s *Seeder) Demo(ctx context.Context) (*Summary, error) {
	sum := &Summary{}

	org, err := s.q.GetOrgBySlug(ctx, DemoSlug)
	if isNoRows(err) {
		return sum, fmt.Errorf("seed: demo org %q not found — create it through the app first", DemoSlug)
	}
	if err != nil {
		return sum, err
	}
	sum.OrgName = org.Name
	sum.OrgID = db.UUIDString(org.ID)

	members, err := s.q.ListOrgMembers(ctx, org.ID)
	if err != nil {
		return sum, err
	}
	var actor pgtype.UUID
	for _, m := range members {
		if m.Role == "org_owner" {
			actor = m.UserID
			break
		}
	}
	if !actor.Valid {
		return sum, fmt.Errorf("seed: demo org has no owner to attribute changes to")
	}
	if owner, err := s.q.GetUserByID(ctx, actor); err == nil {
		sum.OwnerEmail = db.StrVal(owner.Email)
	}

	// ------------------------------------------------------- UAT sign-in --
	// The hand-made owner's password is not knowable from here, so the demo
	// gets its own owner account with a documented one. It is added, never
	// substituted: the original owner keeps their access.
	if err := s.ensureDemoOwner(ctx, org.ID, actor, sum); err != nil {
		return sum, err
	}

	// -------------------------------------- settings: bank + notifications --
	if err := s.demoSettings(ctx, &org, actor, sum); err != nil {
		return sum, err
	}
	lang := settingsLang(org.Settings)

	tpl, err := s.q.GetDefaultContractTemplate(ctx, org.ID)
	if err != nil {
		return sum, fmt.Errorf("seed: demo default template: %w", err)
	}
	periods, err := s.q.ListActivePaymentPeriods(ctx, org.ID)
	if err != nil {
		return sum, err
	}
	byDays := map[int32]sqlc.PaymentPeriod{}
	for _, p := range periods {
		if _, seen := byDays[p.Days]; !seen {
			byDays[p.Days] = p
		}
	}

	props, err := s.q.ListProperties(ctx, sqlc.ListPropertiesParams{OrgID: org.ID, RowLimit: 100})
	if err != nil {
		return sum, err
	}
	propByName := map[string]sqlc.Property{}
	for _, p := range props {
		propByName[p.Name] = sqlc.Property{ID: p.ID, OrgID: org.ID, Name: p.Name, LocationText: p.LocationText}
	}
	sum.Properties = len(props)
	sum.note("demo org has %d propert(ies)", len(props))

	// ---------------------------------------------------------- tenancies --
	for _, d := range demoRenters {
		prop, ok := propByName[d.property]
		if !ok {
			sum.note("skipped %s: property %q not found", d.name, d.property)
			continue
		}
		period, ok := byDays[d.cadence]
		if !ok {
			return sum, fmt.Errorf("seed: demo org has no %d-day payment period", d.cadence)
		}

		renter, created, err := s.ensureRenter(ctx, org.ID, d.phone, d.name, "1234", d.nida, d.kinName, d.kinPhone)
		if err != nil {
			return sum, fmt.Errorf("seed: demo renter %s: %w", d.phone, err)
		}
		sum.Renters += boolInt(created)

		price := d.price
		if price == 0 {
			price = 220_000
		}
		// Resolved whether or not the renter is new, so a re-run still prints
		// the codes the UAT script links to.
		unit, err := s.ensureUnit(ctx, org.ID, actor, prop, d.unit, price, 30)
		if err != nil {
			return sum, fmt.Errorf("seed: demo unit %s/%s: %w", d.property, d.unit, err)
		}
		if unit.created {
			sum.Units++
		}
		sum.UnitCodes = append(sum.UnitCodes, UnitCode{
			Property: prop.Name, Unit: unit.unit.Name, Code: unit.unit.UnitCode,
			Status: unit.unit.Status, Renter: d.name + " — " + d.state,
		})
		if !created {
			sum.note("renter %s already present — left untouched", d.phone)
			continue
		}

		start := s.now.AddDate(0, 0, -d.startOffsetDays)

		if d.state == demoStateLink {
			if err := s.ensureLinkRequest(ctx, org.ID, unit, renter, period, d.termDays); err != nil {
				return sum, fmt.Errorf("seed: demo link request: %w", err)
			}
			sum.LinkRequests++
			continue
		}

		res, err := s.ensureContract(ctx, contractSpec{
			orgID: org.ID, orgName: org.Name, actor: actor, templateID: tpl.ID,
			unit: unit, renter: renter, period: period,
			termDays: d.termDays, start: start, status: d.state,
		})
		if err != nil {
			return sum, fmt.Errorf("seed: demo contract for %s: %w", d.name, err)
		}
		if !res.created {
			continue
		}
		sum.Contracts++
		sum.Schedules += len(res.schedules)

		if d.payFirst && len(res.schedules) > 0 {
			counter := 1 // 1 % 5 == 1 → the helper records this one
			ids, err := s.payDueSchedules(ctx, org.ID, actor, res.contractID,
				res.schedules[:1], renter, unit, org.Name, lang, &counter)
			if err != nil {
				return sum, fmt.Errorf("seed: demo payment: %w", err)
			}
			sum.Payments += len(ids)
		}
	}

	// The backdated tenancy only becomes visibly overdue once the sweep runs —
	// the same statement the hourly job and every schedule read use.
	flipped, err := payment.FlipOverdue(ctx, s.q, org.ID)
	if err != nil {
		return sum, err
	}
	sum.note("overdue sweep flipped %d schedule(s)", flipped)

	sent, err := s.seedNotifications(ctx, org.ID, lang, org.Name)
	if err != nil {
		return sum, err
	}
	sum.Notifications = sent

	// Every unit the demo org still has free, so the UAT script has somewhere
	// to run the scan-and-register flow.
	free, err := s.q.ListUnits(ctx, sqlc.ListUnitsParams{
		OrgID: org.ID, Status: strPtr(unitVacant), RowLimit: 50,
	})
	if err != nil {
		return sum, err
	}
	seen := map[string]bool{}
	for _, c := range sum.UnitCodes {
		seen[c.Code] = true
	}
	for _, u := range free {
		if seen[u.UnitCode] {
			continue
		}
		sum.UnitCodes = append(sum.UnitCodes, UnitCode{
			Property: u.PropertyName, Unit: u.Name, Code: u.UnitCode, Status: u.Status,
		})
	}

	return sum, nil
}

// DemoOwnerEmail / DemoOwnerPassword are the credentials docs/UAT.md signs in
// with. The account is created by seed-demo and carries the owner role, so
// every landlord flow — including inviting staff — is reachable.
const (
	DemoOwnerEmail    = "demo@jjne.test"
	DemoOwnerPassword = "password123"
)

func (s *Seeder) ensureDemoOwner(ctx context.Context, orgID, actor pgtype.UUID, sum *Summary) error {
	if _, err := s.q.GetUserByEmail(ctx, DemoOwnerEmail); err == nil {
		sum.OwnerEmail = DemoOwnerEmail
		return nil
	} else if !isNoRows(err) {
		return err
	}

	hash, err := auth.HashSecret(DemoOwnerPassword)
	if err != nil {
		return err
	}
	email := DemoOwnerEmail
	phone := "+255712000099"
	err = s.inTx(ctx, func(q *sqlc.Queries) error {
		user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
			Kind: auth.KindOrgUser, Phone: &phone, Email: &email,
			FullName: "Demo Owner (UAT)", PasswordHash: &hash,
			EmailVerifiedAt: db.TS(s.now),
		})
		if err != nil {
			return err
		}
		if _, err := q.CreateOrgMember(ctx, sqlc.CreateOrgMemberParams{
			OrgID: orgID, UserID: user.ID, Role: auth.RoleOwner,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionMemberInvite, EntityType: audit.EntityOrgMember,
			EntityID: db.UUIDString(user.ID),
			After:    map[string]any{"email": email, "role": auth.RoleOwner, "seeded": true},
		})
	})
	if err != nil {
		return fmt.Errorf("seed: demo owner: %w", err)
	}
	sum.OwnerEmail = DemoOwnerEmail
	sum.note("UAT owner account created: %s / %s", DemoOwnerEmail, DemoOwnerPassword)
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func strPtr(s string) *string { return &s }

// demoSettings writes the bank account, English SMS language and notification
// block onto the demo org, leaving any value already set alone.
func (s *Seeder) demoSettings(ctx context.Context, org *sqlc.Org, actor pgtype.UUID, sum *Summary) error {
	settings := map[string]any{}
	if len(org.Settings) > 0 {
		if err := json.Unmarshal(org.Settings, &settings); err != nil {
			return fmt.Errorf("seed: demo settings: %w", err)
		}
	}
	changed := false

	if _, ok := settings["bank_account"]; !ok {
		settings["bank_account"] = demoBankAccount
		changed = true
		sum.note("bank account set")
	}
	if lang, _ := settings["sms_language"].(string); lang != "en" {
		settings["sms_language"] = "en"
		changed = true
		sum.note("SMS language set to en")
	}
	if _, ok := settings["notifications"]; !ok {
		settings["notifications"] = map[string]any{
			"sender_name":     "JJNE",
			"send_hour_local": notify.DefaultSendHourLocal,
			"kinds": map[string]any{
				"reminder_7d":       map[string]any{"enabled": true, "offset_days": notify.DefaultReminderOffset},
				"reminder_due":      map[string]any{"enabled": true},
				"overdue_daily":     map[string]any{"enabled": true},
				"thank_you":         map[string]any{"enabled": true},
				"unsigned_reminder": map[string]any{"enabled": true, "after_days": notify.DefaultUnsignedAfterDay},
			},
			"templates": map[string]any{},
		}
		changed = true
		sum.note("notification settings written (sender JJNE, en)")
	}
	if !changed {
		return nil
	}

	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		updated, err := q.UpdateOrg(ctx, sqlc.UpdateOrgParams{Settings: raw, ID: org.ID})
		if err != nil {
			return err
		}
		*org = updated
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(org.ID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionOrgUpdate, EntityType: audit.EntityOrg,
			EntityID: db.UUIDString(org.ID),
			After:    map[string]any{"settings": "demo seed", "seeded": true},
		})
	})
}

// ensureLinkRequest leaves one undecided application in the landlord's inbox
// (FLOWS 3.1), skipping if this renter already has one on the unit.
func (s *Seeder) ensureLinkRequest(ctx context.Context, orgID pgtype.UUID, unit seededUnit, renter sqlc.User, period sqlc.PaymentPeriod, termDays int32) error {
	pending, err := s.q.CountPendingLinkRequest(ctx, sqlc.CountPendingLinkRequestParams{
		UnitID: unit.unit.ID, RenterUserID: renter.ID,
	})
	if err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	startDate := s.now.AddDate(0, 0, 14)
	endDate := startDate.AddDate(0, 0, int(termDays))

	return s.inTx(ctx, func(q *sqlc.Queries) error {
		req, err := q.CreateLinkRequest(ctx, sqlc.CreateLinkRequestParams{
			OrgID: orgID, UnitID: unit.unit.ID, RenterUserID: renter.ID,
			Status: linkPending, PaymentPeriodID: period.ID, TermDays: &termDays,
			StartDate: Date(startDate), EndDate: Date(endDate),
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(renter.ID),
			Action: audit.ActionLinkRequestCreate, EntityType: audit.EntityLinkRequest,
			EntityID: db.UUIDString(req.ID),
			After: map[string]any{
				"unit_id": db.UUIDString(unit.unit.ID), "status": linkPending, "seeded": true,
			},
		})
	})
}
