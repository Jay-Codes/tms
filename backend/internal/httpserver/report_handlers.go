package httpserver

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/auth"
	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/payment"
	"tms/backend/internal/report"
	"tms/backend/internal/validate"
)

// vacantUnitsShown is the length of the "empty longest" list on the summary
// (API.md: at most 20, longest first).
const vacantUnitsShown = 20

// dateOnly is the wire format for every calendar date a report emits.
const dateOnly = "2006-01-02"

// sweepOverdue runs the org-scoped overdue flip that every schedule read
// performs (DECISIONS.md), so a report never quotes a `pending` on a schedule
// that lapsed an hour ago. Failure is logged, not fatal: a slightly stale
// number is better than no dashboard.
func (s *Server) sweepOverdue(r *http.Request, orgID pgtype.UUID) {
	if _, err := payment.FlipOverdue(r.Context(), s.q, orgID); err != nil {
		s.logger.Warn("report overdue sweep failed", "path", r.URL.Path, "error", err)
	}
}

func dateParam(d time.Time) pgtype.Date {
	return pgtype.Date{Time: time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

// ----------------------------------------------------- GET /reports/summary --

func (s *Server) handleReportSummary(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())

	f := validate.Fields{}
	period, err := report.ParsePeriod(strings.TrimSpace(r.URL.Query().Get("period")), time.Now())
	if err != nil {
		f.Add("period", err.Error())
		badRequest(w, f)
		return
	}
	s.sweepOverdue(r, p.OrgID)

	assets, err := s.q.ReportAssets(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "report.summary.assets", err)
		return
	}
	counts, err := s.q.ReportContractCounts(r.Context(), p.OrgID)
	if err != nil {
		s.serverError(w, r, "report.summary.contracts", err)
		return
	}
	sched, err := s.q.ReportSchedulePeriod(r.Context(), sqlc.ReportSchedulePeriodParams{
		OrgID: p.OrgID, FromDate: dateParam(period.From), ToDate: dateParam(period.To),
	})
	if err != nil {
		s.serverError(w, r, "report.summary.schedules", err)
		return
	}
	collected, err := s.q.ReportCollectedPeriod(r.Context(), sqlc.ReportCollectedPeriodParams{
		OrgID: p.OrgID, FromTs: db.TS(period.FromTime()), ToTs: db.TS(period.ToTime()),
	})
	if err != nil {
		s.serverError(w, r, "report.summary.collected", err)
		return
	}
	vacant, err := s.q.ReportVacantUnits(r.Context(), sqlc.ReportVacantUnitsParams{
		OrgID: p.OrgID, RowLimit: vacantUnitsShown,
	})
	if err != nil {
		s.serverError(w, r, "report.summary.vacant", err)
		return
	}

	// Occupancy counts let units against lettable ones. Unlisted and
	// maintenance units are neither: a room taken off the market is not a
	// vacancy the landlord is failing to fill.
	lettable := assets.Occupied + assets.Vacant
	var rate float64
	if lettable > 0 {
		rate = float64(assets.Occupied) / float64(lettable)
	}

	empties := make([]vacantUnitResponse, 0, len(vacant))
	for _, v := range vacant {
		empties = append(empties, vacantUnitResponse{
			UnitID:       db.UUIDString(v.UnitID),
			Name:         v.UnitName,
			PropertyName: v.PropertyName,
			DaysVacant:   int(v.DaysVacant),
		})
	}

	WriteJSON(w, http.StatusOK, reportSummaryResponse{
		Assets: reportAssets{
			Properties:    assets.Properties,
			Units:         assets.Units,
			Occupied:      assets.Occupied,
			Vacant:        assets.Vacant,
			Maintenance:   assets.Maintenance,
			Unlisted:      assets.Unlisted,
			OccupancyRate: rate,
		},
		Renters:   reportRenters{Active: counts.ActiveRenters},
		Contracts: reportContracts{Active: counts.Active, Expiring: counts.Expiring, PendingSignature: counts.PendingSignature},
		Period: reportPeriod{
			From:          period.From.Format(dateOnly),
			To:            period.To.Format(dateOnly),
			Expected:      sched.Expected,
			Collected:     collected,
			Outstanding:   sched.Outstanding,
			OverdueCount:  sched.OverdueCount,
			OverdueAmount: sched.OverdueAmount,
		},
		VacantUnits: empties,
	})
}

// ---------------------------------------------- GET /reports/payment-status --

func (s *Server) handleReportPaymentStatus(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}

	var wantStatus string
	if v := strings.TrimSpace(qs.Get("status")); v != "" {
		wantStatus = f.OneOf("status", strings.ToLower(v), report.Statuses...)
	}
	format := "json"
	if v := strings.TrimSpace(qs.Get("format")); v != "" {
		format = f.OneOf("format", strings.ToLower(v), "json", "csv")
	}
	params := sqlc.ReportTenanciesParams{OrgID: p.OrgID}
	if v := strings.TrimSpace(qs.Get("property_id")); v != "" {
		id, err := db.ParseUUID(v)
		if err != nil {
			f.Add("property_id", "must be a property id (UUID)")
		} else {
			params.PropertyID = id
		}
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	s.sweepOverdue(r, p.OrgID)

	items, err := s.paymentStatusRows(r, p.OrgID, params, wantStatus)
	if err != nil {
		s.serverError(w, r, "report.payment_status", err)
		return
	}
	if format == "csv" {
		writePaymentStatusCSV(w, items)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// paymentStatusRows assembles the per-renter report from four constant-count
// queries joined in memory by contract id — never one query per renter.
func (s *Server) paymentStatusRows(
	r *http.Request, orgID pgtype.UUID, params sqlc.ReportTenanciesParams, wantStatus string,
) ([]paymentStatusRow, error) {
	tenancies, err := s.q.ReportTenancies(r.Context(), params)
	if err != nil {
		return nil, fmt.Errorf("tenancies: %w", err)
	}
	balances, err := s.q.ReportContractBalances(r.Context(), orgID)
	if err != nil {
		return nil, fmt.Errorf("balances: %w", err)
	}
	nextDue, err := s.q.ReportNextDue(r.Context(), orgID)
	if err != nil {
		return nil, fmt.Errorf("next due: %w", err)
	}
	lastPaid, err := s.q.ReportLastPayments(r.Context(), orgID)
	if err != nil {
		return nil, fmt.Errorf("last payments: %w", err)
	}

	byContract := make(map[string]sqlc.ReportContractBalancesRow, len(balances))
	for _, b := range balances {
		byContract[db.UUIDString(b.ContractID)] = b
	}
	dueByContract := make(map[string]sqlc.ReportNextDueRow, len(nextDue))
	for _, d := range nextDue {
		dueByContract[db.UUIDString(d.ContractID)] = d
	}
	paidByContract := make(map[string]time.Time, len(lastPaid))
	for _, l := range lastPaid {
		if l.LastPaymentAt.Valid {
			paidByContract[db.UUIDString(l.ContractID)] = l.LastPaymentAt.Time
		}
	}

	out := make([]paymentStatusRow, 0, len(tenancies))
	for _, t := range tenancies {
		id := db.UUIDString(t.ContractID)
		bal := byContract[id]
		status := report.WorstStatus(bal.OverdueCount, bal.PartialCount, bal.PendingCount)
		if wantStatus != "" && status != wantStatus {
			continue
		}
		row := paymentStatusRow{
			RenterUserID:  db.UUIDString(t.RenterUserID),
			RenterName:    t.RenterName,
			Phone:         db.StrVal(t.RenterPhone),
			UnitName:      t.UnitName,
			PropertyName:  t.PropertyName,
			ContractID:    id,
			Status:        status,
			Outstanding:   bal.Outstanding,
			OverdueAmount: bal.OverdueAmount,
		}
		if d, ok := dueByContract[id]; ok && d.DueDate.Valid {
			date := d.DueDate.Time.Format(dateOnly)
			amount := d.Amount
			row.NextDueDate = &date
			row.NextDueAmount = &amount
		}
		if at, ok := paidByContract[id]; ok {
			t := at.UTC()
			row.LastPaymentAt = &t
		}
		out = append(out, row)
	}
	return out, nil
}

// paymentStatusCSVHeader is the export's column order. It is part of the
// contract: a landlord's spreadsheet formulas point at column letters.
//
//nolint:gochecknoglobals // fixed vocabulary, read-only.
var paymentStatusCSVHeader = []string{
	"renter_name", "phone", "property", "unit", "status",
	"next_due_date", "next_due_amount", "outstanding", "overdue_amount", "last_payment_at",
}

func writePaymentStatusCSV(w http.ResponseWriter, items []paymentStatusRow) {
	filename := "payment-status-" + time.Now().In(report.Zone()).Format(dateOnly) + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	rec := make([]string, len(paymentStatusCSVHeader))
	_ = cw.Write(paymentStatusCSVHeader)
	for _, it := range items {
		// Every free-text cell is defused before it is written: a renter or a
		// property named `=HYPERLINK(...)` is a formula the landlord's
		// spreadsheet would run on open. Commas, quotes and newlines are
		// `encoding/csv`'s job.
		rec[0], rec[1] = report.CSVCell(it.RenterName), report.CSVCell(it.Phone)
		rec[2], rec[3] = report.CSVCell(it.PropertyName), report.CSVCell(it.UnitName)
		rec[4] = it.Status
		rec[5], rec[6] = "", ""
		if it.NextDueDate != nil {
			rec[5] = *it.NextDueDate
			rec[6] = strconv.FormatInt(*it.NextDueAmount, 10)
		}
		rec[7] = strconv.FormatInt(it.Outstanding, 10)
		rec[8] = strconv.FormatInt(it.OverdueAmount, 10)
		rec[9] = ""
		if it.LastPaymentAt != nil {
			rec[9] = it.LastPaymentAt.Format(time.RFC3339)
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
}

// ------------------------------------------------- GET /reports/collections --

func (s *Server) handleReportCollections(w http.ResponseWriter, r *http.Request) {
	if s.dbUnavailable(w) {
		return
	}
	p := auth.MustFromContext(r.Context())
	qs := r.URL.Query()
	f := validate.Fields{}

	group := report.GroupMonth
	if v := strings.TrimSpace(qs.Get("group")); v != "" {
		group = f.OneOf("group", strings.ToLower(v), report.Groups...)
	}
	// The default window is the twelve months ending today — the series a
	// dashboard opens with when the client sends no dates.
	now := time.Now().In(report.Zone())
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	from := to.AddDate(0, -11, 0)
	from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)

	if v := strings.TrimSpace(qs.Get("from")); v != "" {
		t, err := time.Parse(dateOnly, v)
		if err != nil {
			f.Add("from", "must be a date (YYYY-MM-DD)")
		} else {
			from = t
		}
	}
	if v := strings.TrimSpace(qs.Get("to")); v != "" {
		t, err := time.Parse(dateOnly, v)
		if err != nil {
			f.Add("to", "must be a date (YYYY-MM-DD)")
		} else {
			to = t
		}
	}
	if f.Empty() && to.Before(from) {
		f.Add("to", "must not be before `from`")
	}
	buckets := report.Buckets(from, to, group)
	if f.Empty() && len(buckets) > report.MaxBuckets {
		f.Add("group", "the range is too long for this grouping — at most "+
			strconv.Itoa(report.MaxBuckets)+" buckets; narrow the range or group more coarsely")
	}
	if !f.Empty() {
		badRequest(w, f)
		return
	}
	s.sweepOverdue(r, p.OrgID)

	span := report.Period{From: from, To: to}
	expected, err := s.q.ReportCollectionsExpected(r.Context(), sqlc.ReportCollectionsExpectedParams{
		Bucket: group, OrgID: p.OrgID, FromDate: dateParam(from), ToDate: dateParam(to),
	})
	if err != nil {
		s.serverError(w, r, "report.collections.expected", err)
		return
	}
	collected, err := s.q.ReportCollectionsCollected(r.Context(), sqlc.ReportCollectionsCollectedParams{
		Bucket: group, OrgID: p.OrgID, FromTs: db.TS(span.FromTime()), ToTs: db.TS(span.ToTime()),
	})
	if err != nil {
		s.serverError(w, r, "report.collections.collected", err)
		return
	}

	expectedBy := map[string]int64{}
	for _, e := range expected {
		expectedBy[e.BucketStart.Time.Format(dateOnly)] = e.Expected
	}
	collectedBy := map[string]int64{}
	for _, c := range collected {
		collectedBy[c.BucketStart.Time.Format(dateOnly)] = c.Collected
	}

	items := make([]collectionBucket, 0, len(buckets))
	var totals collectionTotals
	for _, b := range buckets {
		key := b.Format(dateOnly)
		bucket := collectionBucket{Start: key, Expected: expectedBy[key], Collected: collectedBy[key]}
		totals.Expected += bucket.Expected
		totals.Collected += bucket.Collected
		items = append(items, bucket)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"buckets": items, "totals": totals})
}
