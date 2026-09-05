package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/tz"
)

// SchedulerInterval is how often the ticker derives due sends from Postgres
// (SPEC §2.2: a 5-minute scan inside the API binary).
const SchedulerInterval = 5 * time.Minute

// LocalZone is the wall clock every org's send hour is read against
// (internal/tz, shared with the reports).
const LocalZone = tz.LocalZone

// Scheduler defaults, overridable per org through
// `orgs.settings.notifications` (API.md Phase 6).
const (
	DefaultSendHourLocal    = 9
	DefaultReminderOffset   = 7
	DefaultUnsignedAfterDay = 7
)

// dateLayout is the date form the dedupe keys and templates use.
const dateLayout = "2006-01-02"

// SchedulerSettings is the slice of an org's settings the scheduler reads. The
// HTTP layer owns the full shape (and its validation); this is what the sweep
// needs to decide whether, when and in what words to send.
type SchedulerSettings struct {
	// Language is the org's default for renters who have no locale of their
	// own (`orgs.settings.sms_language`). A recipient's own `users.locale`
	// wins over it — see LanguageFor.
	Language       string
	SendHourLocal  int
	Overrides      Overrides
	Reminder7d     bool
	ReminderOffset int
	ReminderDue    bool
	OverdueDaily   bool
	Unsigned       bool
	UnsignedAfter  int
}

// SettingsFor resolves one org's scheduler settings from its raw
// `orgs.settings` JSON. It is a function value so the HTTP layer — which owns
// the settings shape — supplies the parser and the scheduler stays free of it.
type SettingsFor func(raw []byte) SchedulerSettings

// Options tune one sweep.
type Options struct {
	// Date overrides the org-local date the sweep runs for
	// (`POST /admin/jobs/notifications {date}`). Zero means "derive it from
	// Now in the org's zone".
	Date time.Time
	// ForceHour ignores the org's send-hour gate, so a tester does not have to
	// wait until 09:00 to watch the timeline fire.
	ForceHour bool
	// BaseURL is the origin the `{{link}}` variable is built against.
	BaseURL string
	// Settings parses one org's settings blob; required.
	Settings SettingsFor
}

// Result reports what one sweep queued.
type Result struct {
	// Queued counts the new notification_log rows per kind. A kind that
	// produced nothing is absent, so a repeat run on the same day reports an
	// empty map rather than zeros — the dedupe keys did their job.
	Queued map[string]int
	// IDs are the rows to push onto Redis once the caller is done.
	IDs []string
}

func newResult() *Result { return &Result{Queued: map[string]int{}} }

func (r *Result) add(kind, id string) {
	r.Queued[kind]++
	if id != "" {
		r.IDs = append(r.IDs, id)
	}
}

// Total counts every row the sweep queued.
func (r Result) Total() int {
	n := 0
	for _, v := range r.Queued {
		n += v
	}
	return n
}

// RunOnce derives the sends due at `now` from Postgres and writes them to
// notification_log.
//
// It is the whole of the scheduler's logic and takes no Redis: the caller
// enqueues Result.IDs afterwards, which keeps the sweep testable against a
// bare database and keeps Redis exactly where SPEC §2.2 puts it — carrying the
// work item, never the truth.
//
// Every send is keyed by `{kind}:{entity}:{date}`, so a second run on the same
// day queues nothing new. The date is the org's own local date, because that
// is the day the renter is living in.
func RunOnce(ctx context.Context, q *sqlc.Queries, now time.Time, opt Options) (Result, error) {
	res := newResult()
	if q == nil {
		return *res, fmt.Errorf("notify: scheduler needs a database")
	}
	if opt.Settings == nil {
		return *res, fmt.Errorf("notify: scheduler needs a settings parser")
	}

	orgs, err := q.ListActiveOrgs(ctx)
	if err != nil {
		return *res, fmt.Errorf("notify: list orgs: %w", err)
	}
	loc := localZone()

	for _, org := range orgs {
		set := opt.Settings(org.Settings)
		local := now.In(loc)
		if !opt.ForceHour && local.Hour() < set.SendHourLocal {
			continue // the org's morning has not come round yet
		}
		today := LocalDate(local)
		if !opt.Date.IsZero() {
			today = LocalDate(opt.Date)
		}

		if err := runForOrg(ctx, q, org, set, today, opt, res); err != nil {
			return *res, err
		}
	}
	return *res, nil
}

// runForOrg queues one org's due messages for one local day.
func runForOrg(
	ctx context.Context, q *sqlc.Queries, org sqlc.ListActiveOrgsRow,
	set SchedulerSettings, today time.Time, opt Options, res *Result,
) error {
	date := today.Format(dateLayout)
	orgID := db.UUIDString(org.ID)

	// reminder_7d — the instalment falling due `offset_days` from today.
	if set.Reminder7d {
		offset := set.ReminderOffset
		if offset < 0 {
			offset = DefaultReminderOffset
		}
		rows, err := q.ListScheduleReminderTargets(ctx, sqlc.ListScheduleReminderTargetsParams{
			OrgID:    org.ID,
			Statuses: []string{"pending", "partial"},
			DueOn:    pgtype.Date{Time: today.AddDate(0, 0, offset), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("notify: reminder_7d targets: %w", err)
		}
		queueScheduleRows(ctx, q, KindReminder7d, orgID, org.DisplayName, date, set, rows, res)
	}

	// reminder_due — the instalment falling due today.
	if set.ReminderDue {
		rows, err := q.ListScheduleReminderTargets(ctx, sqlc.ListScheduleReminderTargetsParams{
			OrgID:    org.ID,
			Statuses: []string{"pending", "partial"},
			DueOn:    pgtype.Date{Time: today, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("notify: reminder_due targets: %w", err)
		}
		queueScheduleRows(ctx, q, KindReminderDue, orgID, org.DisplayName, date, set, rows, res)
	}

	// overdue_daily — every unresolved row past its due date, once a day until
	// it is paid or waived (SPEC §6).
	if set.OverdueDaily {
		rows, err := q.ListScheduleReminderTargets(ctx, sqlc.ListScheduleReminderTargetsParams{
			OrgID:     org.ID,
			Statuses:  []string{"overdue"},
			DueBefore: pgtype.Date{Time: today, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("notify: overdue targets: %w", err)
		}
		queueScheduleRows(ctx, q, KindOverdueDaily, orgID, org.DisplayName, date, set, rows, res)
	}

	// unsigned_reminder — a contract still waiting on the renter's signature.
	if set.Unsigned {
		after := set.UnsignedAfter
		if after < 0 {
			after = DefaultUnsignedAfterDay
		}
		rows, err := q.ListUnsignedContractsForOrg(ctx, sqlc.ListUnsignedContractsForOrgParams{
			OrgID:         org.ID,
			CreatedBefore: db.TS(today.AddDate(0, 0, -after).Add(24 * time.Hour)),
		})
		if err != nil {
			return fmt.Errorf("notify: unsigned targets: %w", err)
		}
		for _, row := range rows {
			phone := db.StrVal(row.RenterPhone)
			if phone == "" {
				continue
			}
			contractID := db.UUIDString(row.ID)
			// The renter's own locale decides the language; the org's
			// setting is only the fallback for a renter who has never
			// expressed one (SPEC §3.2).
			lang := LanguageFor(row.RenterLocale, set.Language)
			body := Render(KindUnsignedReminder, lang, Vars{
				Name:     row.RenterName,
				Unit:     row.UnitName,
				Property: row.PropertyName,
				Org:      org.DisplayName,
				Link:     contractLink(opt.BaseURL, contractID),
			}, set.Overrides)
			queueOne(ctx, q, res, Msg{
				OrgID: orgID, UserID: db.UUIDString(row.RenterUserID),
				Kind: KindUnsignedReminder,
				// API.md fixes this key as `unsigned:` — shorter than the kind
				// it writes, and the contract, so it is used verbatim.
				DedupeKey: "unsigned:" + contractID + ":" + date,
				Phone:     phone, Body: body, Language: lang,
			})
		}
	}
	return nil
}

// queueScheduleRows renders and queues one kind's schedule-derived messages.
func queueScheduleRows(
	ctx context.Context, q *sqlc.Queries, kind, orgID, orgName, date string,
	set SchedulerSettings, rows []sqlc.ListScheduleReminderTargetsRow, res *Result,
) {
	for _, row := range rows {
		phone := db.StrVal(row.RenterPhone)
		if phone == "" {
			continue // nothing to send to; the schedule itself still stands
		}
		outstanding := row.Amount - row.PaidAmount
		if outstanding < 0 {
			outstanding = 0
		}
		vars := Vars{
			Name:     row.RenterName,
			Amount:   FormatTZS(outstanding),
			DueDate:  row.DueDate.Time.Format(dateLayout),
			Property: row.PropertyName,
			Unit:     row.UnitName,
			Org:      orgName,
		}
		if row.NextDueDate.Valid {
			vars.NextDueDate = row.NextDueDate.Time.Format(dateLayout)
		}
		lang := LanguageFor(row.RenterLocale, set.Language)
		queueOne(ctx, q, res, Msg{
			OrgID: orgID, UserID: db.UUIDString(row.RenterUserID), Kind: kind,
			DedupeKey: kind + ":" + db.UUIDString(row.ID) + ":" + date,
			Phone:     phone,
			Body:      Render(kind, lang, vars, set.Overrides),
			Language:  lang,
		})
	}
}

// queueOne writes a message, counting it unless the dedupe key already existed.
func queueOne(ctx context.Context, q *sqlc.Queries, res *Result, m Msg) {
	id, err := Queue(ctx, q, m)
	if errors.Is(err, ErrDuplicate) {
		return // already sent today; that is the whole point of the key
	}
	if err != nil {
		slog.Default().Warn("notification not queued", "kind", m.Kind, "dedupe_key", m.DedupeKey, "error", err)
		return
	}
	res.add(m.Kind, id)
}

// contractLink builds the renter-facing link to a contract.
func contractLink(baseURL, contractID string) string {
	return strings.TrimRight(baseURL, "/") + "/enduser/contract/" + contractID
}

// LocalDate is the calendar day t names, as a UTC midnight. t must already be
// in the org's zone (internal/tz).
func LocalDate(t time.Time) time.Time { return tz.LocalDate(t) }

// localZone resolves the org wall clock, falling back to a fixed UTC+3 when the
// host image ships without tzdata.
func localZone() *time.Location { return tz.Zone() }

// FormatTZS renders an amount in the money form the SMS templates use.
func FormatTZS(amount int64) string {
	digits := fmt.Sprintf("%d", amount)
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "TZS -" + b.String()
	}
	return "TZS " + b.String()
}

// RunScheduler runs the sweep once at startup and then every
// SchedulerInterval until ctx is cancelled.
//
// A failed sweep is logged, never fatal: the next tick derives the same work
// from Postgres, and the dedupe keys make the repeat harmless (SPEC §2.2).
func RunScheduler(
	ctx context.Context, pool *db.Pool, rdb *redis.Client, logger *slog.Logger, opt Options,
) {
	if logger == nil {
		logger = slog.Default()
	}
	if pool == nil {
		logger.Warn("notification scheduler not started: postgres unavailable")
		return
	}
	q := sqlc.New(pool)

	run := func() {
		res, err := RunOnce(ctx, q, time.Now(), opt)
		if err != nil {
			logger.Error("notification sweep failed", "error", err)
			// Partial results are still durable rows; push what was written.
		}
		if len(res.IDs) > 0 {
			Enqueue(ctx, rdb, logger, res.IDs...)
			logger.Info("notification sweep queued messages", "queued", res.Queued)
		}
	}
	run()

	ticker := time.NewTicker(SchedulerInterval)
	defer ticker.Stop()
	logger.Info("notification scheduler started", "interval", SchedulerInterval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
