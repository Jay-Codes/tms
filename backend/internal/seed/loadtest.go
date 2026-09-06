package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/audit"
	"tms/backend/internal/contract"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/payment"
	"tms/backend/internal/validate"
)

// LoadTestOptions parameterises the load-pass fixture (PLAN Phase 8: 1 org,
// 5 properties, 50 units, 40 renters).
type LoadTestOptions struct {
	OrgName    string
	OwnerEmail string
	Password   string
	Properties int
	Units      int
	Renters    int
	Contracts  int
}

// DefaultLoadTestOptions is what `make seed` runs.
func DefaultLoadTestOptions() LoadTestOptions {
	return LoadTestOptions{
		OrgName:    "Load Test Estates",
		OwnerEmail: "load@tms.local",
		Password:   "password123",
		Properties: 5,
		Units:      50,
		Renters:    40,
		Contracts:  35,
	}
}

// propertyNames are the five load-test estates, in order.
var propertyNames = []struct{ name, location string }{
	{"Load Estate — Mbezi", "Mbezi Beach, Kinondoni, Dar es Salaam"},
	{"Load Estate — Kigamboni", "Kigamboni, Temeke, Dar es Salaam"},
	{"Load Estate — Sinza", "Sinza Mori, Ubungo, Dar es Salaam"},
	{"Load Estate — Tegeta", "Tegeta Nyuki, Kinondoni, Dar es Salaam"},
	{"Load Estate — Kisarawe", "Kisarawe II, Temeke, Dar es Salaam"},
}

// firstNames / lastNames build plausible Tanzanian renter names without
// pulling in a data file.
var firstNames = []string{
	"Asha", "Baraka", "Cecilia", "Daudi", "Editha", "Frank", "Gloria", "Hamisi",
	"Imani", "Juma", "Kelvin", "Lucy", "Mwajuma", "Neema", "Omary", "Pendo",
	"Rehema", "Salum", "Tumaini", "Upendo",
}

var lastNames = []string{
	"Mwakalinga", "Kimaro", "Shirima", "Mushi", "Ngassa", "Mbwana", "Lyimo",
	"Nyerere", "Kileo", "Massawe", "Mrema", "Sanga", "Chuwa", "Kessy",
}

// The start spread for the load-test tenancies (PLAN2 Phase 15). 35 contracts
// stepping 11 days apart from 380 days ago put the first start just over a
// year back and the last inside the current month.
const (
	loadTestStartSpreadDays = 380
	loadTestStartStepDays   = 11
)

// renterLocale splits a seeded org's renters roughly 60/40 Swahili/English —
// the mix a Dar es Salaam landlord actually has, and enough English speakers
// that a bulk send has to render both languages.
func renterLocale(i int) string {
	if i%5 < 3 {
		return "sw"
	}
	return "en"
}

// live is one tenancy the run created, kept so the payment, lifecycle and
// termination passes below can find it again without a re-query.
type live struct {
	contractID pgtype.UUID
	renter     sqlc.User
	unit       seededUnit
	schedules  []sqlc.PaymentSchedule
}

// LoadTest seeds (or tops up) the load-pass org.
func (s *Seeder) LoadTest(ctx context.Context, opts LoadTestOptions) (*Summary, error) {
	sum := &Summary{}
	slug := validate.Slugify(opts.OrgName)

	boot, err := s.ensureOrg(ctx, opts.OrgName, slug, "Load Test Owner",
		opts.OwnerEmail, "+255700000001", opts.Password, sum)
	if err != nil {
		return sum, err
	}
	orgID := boot.org.ID
	actor := boot.owner.ID
	lang := settingsLang(boot.org.Settings)

	// ------------------------------------------------------- properties --
	props := make([]sqlc.Property, 0, opts.Properties)
	for i := 0; i < opts.Properties; i++ {
		p := propertyNames[i%len(propertyNames)]
		name := p.name
		if i >= len(propertyNames) {
			name = fmt.Sprintf("%s %d", p.name, i+1)
		}
		prop, created, err := s.ensureProperty(ctx, orgID, actor, name, p.location)
		if err != nil {
			return sum, fmt.Errorf("seed: property %q: %w", name, err)
		}
		if created {
			sum.Properties++
		}
		props = append(props, prop)
	}

	// ------------------------------------------------------------ units --
	units := make([]seededUnit, 0, opts.Units)
	for i := 0; i < opts.Units; i++ {
		prop := props[i%len(props)]
		name := fmt.Sprintf("Unit %03d", i+1)
		// A spread of rents so reports and the vacancy board are not flat.
		price := int64(120_000 + (i%9)*30_000)
		u, err := s.ensureUnit(ctx, orgID, actor, prop, name, price, 30)
		if err != nil {
			return sum, fmt.Errorf("seed: unit %q: %w", name, err)
		}
		if u.created {
			sum.Units++
		}
		units = append(units, u)
	}

	// ---------------------------------------------------------- renters --
	renters := make([]sqlc.User, 0, opts.Renters)
	for i := 0; i < opts.Renters; i++ {
		phone := fmt.Sprintf("+25576%07d", i+1)
		name := fmt.Sprintf("%s %s", firstNames[i%len(firstNames)], lastNames[(i/3)%len(lastNames)])
		nida := fmt.Sprintf("19900101%08d", i+1)
		kinPhone := fmt.Sprintf("+25577%07d", i+1)
		u, created, err := s.ensureRenter(ctx, orgID, phone, name, "1234", nida,
			lastNames[(i+5)%len(lastNames)]+" (next of kin)", kinPhone, renterLocale(i))
		if err != nil {
			return sum, fmt.Errorf("seed: renter %s: %w", phone, err)
		}
		if created {
			sum.Renters++
		}
		renters = append(renters, u)
	}

	// -------------------------------------------------------- contracts --
	// Periods vary and start dates walk back over the last 120 days, so the
	// fixture holds paid, partial, overdue and pending schedules at once.
	cadences := []int32{30, 90, 180}
	terms := []int32{360, 180, 365}
	// Starts walk back a full year rather than the 120 days Part 1 needed
	// (PLAN2 Phase 15). Two things follow, and both are the point:
	//
	//   - the payments the loop below records land on their schedules' due
	//     dates, so the money is spread across twelve months and the revenue
	//     series has twelve buckets with something in them rather than four;
	//   - the earliest 180-day tenancies have already run out, so the fixture
	//     carries `ended` contracts and freed units — the states the occupancy
	//     series and the vacancy board are read for.
	//
	// The step is chosen so the last contract starts within the current month
	// and the first a little over a year ago.

	var actives []live

	n := opts.Contracts
	if n > len(units) {
		n = len(units)
	}
	if n > len(renters) {
		n = len(renters)
	}
	for i := 0; i < n; i++ {
		cadence := cadences[i%len(cadences)]
		period, ok := boot.periods[cadence]
		if !ok {
			return sum, fmt.Errorf("seed: org has no %d-day payment period", cadence)
		}
		start := s.now.AddDate(0, 0, -(loadTestStartSpreadDays - i*loadTestStartStepDays))
		res, err := s.ensureContract(ctx, contractSpec{
			orgID: orgID, orgName: boot.org.Name, actor: actor, templateID: boot.tplID,
			unit: units[i], renter: renters[i], period: period,
			termDays: terms[i%len(terms)], start: start, status: contractActive,
		})
		if err != nil {
			return sum, fmt.Errorf("seed: contract %d: %w", i, err)
		}
		if !res.created {
			continue
		}
		sum.Contracts++
		sum.Schedules += len(res.schedules)
		actives = append(actives, live{
			contractID: res.contractID, renter: renters[i],
			unit: units[i], schedules: res.schedules,
		})
	}

	// ----------------------------------------------------- link requests --
	// Every renter the contract loop did not reach applies for one of the
	// spare units instead. That fills the landlord's approval inbox (FLOWS 3)
	// and, just as usefully, leaves no seeded renter without a tie to this org
	// — which is what lets -reset take them away again.
	for i := n; i < len(renters); i++ {
		unitIdx := i
		if unitIdx >= len(units) {
			break
		}
		period := boot.periods[30]
		made, err := s.ensureLinkRequest(ctx, orgID, units[unitIdx], renters[i], period, 180)
		if err != nil {
			return sum, fmt.Errorf("seed: link request for renter %d: %w", i, err)
		}
		sum.LinkRequests += boolInt(made)
	}

	// --------------------------------------------------------- payments --
	grace := settingsGraceDays(boot.org.Settings)
	paidCounter := 0
	for ci, c := range actives {
		payments, err := s.payDueSchedules(ctx, orgID, actor, c.contractID, c.schedules,
			c.renter, c.unit, boot.org.Name, lang, &paidCounter)
		if err != nil {
			return sum, fmt.Errorf("seed: payments for contract %d: %w", ci, err)
		}
		sum.Payments += len(payments)

		// A handful of mistakes, reversed the way POST /payments/{id}/reverse
		// does it: the payment is marked reversed and its allocations debited.
		if ci%11 == 3 && len(payments) > 0 {
			if err := s.reversePayment(ctx, orgID, actor, payments[0], grace); err != nil {
				return sum, fmt.Errorf("seed: reverse payment: %w", err)
			}
			sum.Reversed++
		}
	}

	// ---------------------------------------------- contract lifecycle --
	//
	// The backdated starts mean some tenancies have already run their term.
	// The same hourly sweep the API runs is what turns those into `ended` and
	// hands their units back to the vacancy board — running it here rather
	// than writing the status directly keeps the fixture in a state the
	// product could actually have reached. It is a platform-wide sweep by
	// design (the dates it acts on are the same in every org).
	life, err := contract.RunLifecycle(ctx, s.pool)
	if err != nil {
		return sum, fmt.Errorf("seed: contract lifecycle: %w", err)
	}
	sum.note("lifecycle sweep: %d ended, %d expiring, %d unit(s) freed",
		life.Ended, life.Expiring, life.Freed)

	// One tenancy ended early by the landlord rather than by the calendar.
	// `terminated` is a status no sweep produces, and the reports and the
	// contract list both have a branch for it.
	if err := s.terminateOne(ctx, orgID, actor, actives, sum); err != nil {
		return sum, err
	}

	// ------------------------------------------------- overdue + notices --
	flipped, err := payment.FlipOverdue(ctx, s.q, orgID)
	if err != nil {
		return sum, err
	}
	sum.note("overdue sweep flipped %d schedule(s)", flipped)

	sent, err := s.seedNotifications(ctx, orgID, lang, boot.org.Name)
	if err != nil {
		return sum, fmt.Errorf("seed: notifications: %w", err)
	}
	sum.Notifications = sent

	// A few vacancies and one maintenance unit so the vacancy board has rows.
	if len(units) > n {
		if _, err := s.q.SetUnitStatusDerived(ctx, sqlc.SetUnitStatusDerivedParams{
			Status: unitVacant, OrgID: orgID, ID: units[n].unit.ID,
		}); err != nil {
			return sum, err
		}
	}

	// ------------------------------------------------- Part 2 fixture --
	//
	// Twelve months of expenses, a credit balance with a ledger row to explain
	// it, and a couple of held messages: what the Part 2 screens and the
	// reports load pass read (PLAN2 Phase 15).
	if err := s.ensureExpenses(ctx, orgID, actor, props, sum); err != nil {
		return sum, err
	}
	if err := s.ensureSMSCredits(ctx, orgID, s.platformAdminID(ctx), SeededCredits, sum); err != nil {
		return sum, err
	}
	if err := s.ensureHeldMessages(ctx, orgID, lang, boot.org.Name, sum); err != nil {
		return sum, err
	}

	sum.UnitCodes = collectCodes(units, 6)
	return sum, nil
}

// terminateOne ends one seeded tenancy the way POST /contracts/{id}/terminate
// does: a reason, an effective date, an audit row. It picks a contract that is
// still live and skips silently when there is none — a re-run finds them all
// dealt with, which is the idempotent answer.
func (s *Seeder) terminateOne(ctx context.Context, orgID, actor pgtype.UUID,
	actives []live, sum *Summary,
) error {
	if len(actives) < 3 {
		return nil
	}
	target := actives[2]
	reason := "Mpangaji alihama kabla ya muda — makubaliano ya pande zote"
	effective := s.now.AddDate(0, 0, -14)

	err := s.inTx(ctx, func(q *sqlc.Queries) error {
		row, err := q.TerminateContract(ctx, sqlc.TerminateContractParams{
			TerminationReason: &reason, TerminationEffectiveDate: Date(effective),
			OrgID: orgID, ID: target.contractID,
		})
		if isNoRows(err) {
			return nil // already ended or terminated by an earlier run
		}
		if err != nil {
			return err
		}
		if _, err := q.SetUnitStatusDerived(ctx, sqlc.SetUnitStatusDerivedParams{
			Status: unitVacant, OrgID: orgID, ID: target.unit.unit.ID,
		}); err != nil {
			return err
		}
		sum.note("contract on %s terminated (%s)", target.unit.unit.Name, effective.Format(DateLayout))
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionContractTerminate, EntityType: audit.EntityContract,
			EntityID: db.UUIDString(target.contractID),
			Before:   map[string]any{"status": contractActive},
			After: map[string]any{
				"status": row.Status, "reason": reason,
				"effective_date": effective.Format(DateLayout), "seeded": true,
			},
		})
	})
	if err != nil {
		return fmt.Errorf("seed: terminate contract: %w", err)
	}
	return nil
}

// payDueSchedules records payments against the schedules whose due date has
// passed, covering roughly 60% of them — three in every five, with every
// seventh left part-paid so `partial` appears in the fixture too.
//
// The split itself is internal/payment.Allocate, the same function the handler
// calls; the seeder only decides how much money arrives.
func (s *Seeder) payDueSchedules(
	ctx context.Context,
	orgID, actor, contractID pgtype.UUID,
	schedules []sqlc.PaymentSchedule,
	renter sqlc.User, unit seededUnit, orgName, lang string,
	counter *int,
) ([]pgtype.UUID, error) {
	var recorded []pgtype.UUID

	for idx, sc := range schedules {
		if sc.DueDate.Time.After(s.now) {
			break // schedules are generated in due order
		}
		*counter++
		if *counter%5 >= 3 {
			continue // ~40% left unpaid → pending, then overdue
		}
		amount := sc.Amount
		if *counter%7 == 0 {
			amount = sc.Amount * 6 / 10 // a part payment
		}
		if amount <= 0 {
			continue
		}

		method := []string{"cash", "bank_transfer", "mobile_money_manual"}[*counter%3]
		paidAt := sc.DueDate.Time.AddDate(0, 0, 1)
		if paidAt.After(s.now) {
			paidAt = s.now
		}
		ref := fmt.Sprintf("SEED-%s-%02d", db.UUIDString(contractID)[:8], idx+1)

		var payID pgtype.UUID
		err := s.inTx(ctx, func(q *sqlc.Queries) error {
			rows, err := q.LockSchedulesForContract(ctx, sqlc.LockSchedulesForContractParams{
				OrgID: orgID, ContractID: contractID,
			})
			if err != nil {
				return err
			}
			allocSchedules := make([]payment.Schedule, 0, len(rows))
			target := -1
			for i, row := range rows {
				allocSchedules = append(allocSchedules, payment.Schedule{
					ID: db.UUIDString(row.ID), Amount: row.Amount,
					PaidAmount: row.PaidAmount, Status: row.Status,
					DueDate: row.DueDate.Time.Format(DateLayout),
				})
				if row.ID == sc.ID {
					target = i
				}
			}
			if target < 0 || allocSchedules[target].Settled() {
				return nil
			}
			applied, err := payment.Allocate(amount, allocSchedules[target],
				allocSchedules[target+1:], true)
			if err != nil {
				return err
			}
			pay, err := q.CreatePayment(ctx, sqlc.CreatePaymentParams{
				OrgID: orgID, ContractID: contractID, ScheduleID: sc.ID,
				Amount: amount, Method: method, Reference: db.Str(ref),
				PaidAt: db.TS(paidAt), RecordedByUserID: actor,
				Note: db.Str("seeded payment"),
			})
			if err != nil {
				return err
			}
			payID = pay.ID
			for _, a := range applied {
				schedID := db.MustUUID(a.ScheduleID)
				if _, err := q.CreatePaymentAllocation(ctx, sqlc.CreatePaymentAllocationParams{
					OrgID: orgID, PaymentID: pay.ID, ScheduleID: schedID, Amount: a.Amount,
				}); err != nil {
					return err
				}
				if _, err := q.ApplyPaymentToSchedule(ctx, sqlc.ApplyPaymentToScheduleParams{
					PaidAmount: a.NewPaid, OrgID: orgID, ID: schedID,
				}); err != nil {
					return err
				}
			}
			if err := audit.Record(ctx, q, audit.Entry{
				OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
				Action: audit.ActionPaymentRecord, EntityType: audit.EntityPayment,
				EntityID: db.UUIDString(pay.ID),
				After: map[string]any{
					"contract_id": db.UUIDString(contractID), "amount": amount,
					"method": method, "seeded": true,
				},
			}); err != nil {
				return err
			}
			_, err = s.notifyRow(ctx, q, orgID, renter.ID, notify.KindThankYou, lang,
				db.StrVal(renter.Phone), "seed:thank_you:"+db.UUIDString(pay.ID),
				notify.Vars{
					Name: renter.FullName, Amount: notify.FormatTZS(amount),
					Unit: unit.unit.Name, Property: unit.property.Name, Org: orgName,
				}, paidAt)
			return err
		})
		if err != nil {
			return recorded, err
		}
		if payID.Valid {
			recorded = append(recorded, payID)
		}
	}
	return recorded, nil
}

// reversePayment mirrors POST /payments/{id}/reverse.
func (s *Seeder) reversePayment(ctx context.Context, orgID, actor, paymentID pgtype.UUID, grace int32) error {
	reason := "seeded reversal: recorded against the wrong tenancy"
	return s.inTx(ctx, func(q *sqlc.Queries) error {
		allocations, err := q.ListAllocationsForPayments(ctx, sqlc.ListAllocationsForPaymentsParams{
			OrgID: orgID, PaymentIds: []pgtype.UUID{paymentID},
		})
		if err != nil {
			return err
		}
		reversed, err := q.ReversePayment(ctx, sqlc.ReversePaymentParams{
			ReversalReason: &reason, ReversedByUserID: actor, OrgID: orgID, ID: paymentID,
		})
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, a := range allocations {
			if _, err := q.UnapplyPaymentFromSchedule(ctx, sqlc.UnapplyPaymentFromScheduleParams{
				Delta: a.Amount, GraceDays: grace, OrgID: orgID, ID: a.ScheduleID,
			}); err != nil {
				return err
			}
		}
		return audit.Record(ctx, q, audit.Entry{
			OrgID: db.UUIDString(orgID), ActorUserID: db.UUIDString(actor),
			Action: audit.ActionPaymentReverse, EntityType: audit.EntityPayment,
			EntityID: db.UUIDString(paymentID),
			After:    map[string]any{"status": reversed.Status, "reason": reason, "seeded": true},
		})
	})
}

// seedNotifications appends the reminder / overdue rows the scheduler would
// have written for the schedules that are already due, so the notification log
// screen has history. Dedupe keys make it idempotent.
func (s *Seeder) seedNotifications(ctx context.Context, orgID pgtype.UUID, lang, orgName string) (int, error) {
	rows, err := s.q.ListSchedules(ctx, sqlc.ListSchedulesParams{OrgID: orgID, RowLimit: 400})
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, sc := range rows {
		if sc.DueDate.Time.After(s.now) {
			continue
		}
		kind := notify.KindReminderDue
		if sc.Status == payment.StatusOverdue {
			kind = notify.KindOverdueDaily
		}
		due := sc.DueDate.Time.Format(DateLayout)
		dedupe := fmt.Sprintf("seed:%s:%s:%s", kind, db.UUIDString(sc.ID), due)

		phone := ""
		if u, err := s.q.GetUserByID(ctx, sc.RenterUserID); err == nil {
			phone = db.StrVal(u.Phone)
		}
		if phone == "" {
			continue
		}
		ok, err := s.inTxNotify(ctx, orgID, sc.RenterUserID, kind, lang, phone, dedupe,
			notify.Vars{
				Name: sc.RenterName, Amount: notify.FormatTZS(sc.Amount - sc.PaidAmount),
				DueDate: due, Unit: sc.UnitName, Property: sc.PropertyName, Org: orgName,
			}, sc.DueDate.Time)
		if err != nil {
			return sent, err
		}
		if ok {
			sent++
		}
	}
	return sent, nil
}

func (s *Seeder) inTxNotify(ctx context.Context, orgID, userID pgtype.UUID, kind, lang, phone, dedupe string, vars notify.Vars, at time.Time) (bool, error) {
	var ok bool
	err := s.inTx(ctx, func(q *sqlc.Queries) error {
		var err error
		ok, err = s.notifyRow(ctx, q, orgID, userID, kind, lang, phone, dedupe, vars, at)
		return err
	})
	return ok, err
}

// collectCodes returns up to n unit codes for the run's closing report.
func collectCodes(units []seededUnit, n int) []UnitCode {
	out := make([]UnitCode, 0, n)
	for i, u := range units {
		if i >= n {
			break
		}
		out = append(out, UnitCode{
			Property: u.property.Name, Unit: u.unit.Name,
			Code: u.unit.UnitCode, Status: u.unit.Status,
		})
	}
	return out
}
