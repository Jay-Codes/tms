package notify_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/testutil"
)

// countingProvider records how many sends it saw and can be told to fail.
type countingProvider struct {
	mu      sync.Mutex
	calls   int32
	senders []string
	fail    bool
	failFor int32 // fail the first N calls, then succeed
}

func (p *countingProvider) Send(_ context.Context, _, _, senderName string) (string, error) {
	n := atomic.AddInt32(&p.calls, 1)
	p.mu.Lock()
	p.senders = append(p.senders, senderName)
	p.mu.Unlock()
	if p.fail || n <= p.failFor {
		return "", errors.New("provider unavailable")
	}
	return "msg-ok", nil
}

func (p *countingProvider) count() int { return int(atomic.LoadInt32(&p.calls)) }

// TestClaimIsAtomic is the whole concurrency story of the worker pool: two
// workers holding the same id must produce one SMS, because only one of them
// wins the `WHERE status='queued'` update.
func TestClaimIsAtomic(t *testing.T) {
	pool := testutil.Pool(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx := context.Background()

	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindReminderDue,
		DedupeKey: "reminder_due:claim:2026-09-05", Phone: "+255799000001", Body: "due today",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	parsed, err := parseID(id)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}

	// Both workers race for the same row.
	var (
		wg      sync.WaitGroup
		won     int32
		results = make([]error, 2)
	)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := q.ClaimNotification(ctx, parsed)
			results[n] = err
			if err == nil {
				atomic.AddInt32(&won, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if won != 1 {
		t.Fatalf("%d workers claimed the same row, want exactly 1 (errors: %v)", won, results)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM notification_log WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "sending" {
		t.Errorf("status after the claim = %q, want sending", status)
	}
}

// TestClaimResolvesTheOrgSenderName: the sender ID travels with the claim, so
// the send path is one round trip and a live message carries the landlord's
// own approved name.
func TestClaimResolvesTheOrgSenderName(t *testing.T) {
	pool := testutil.Pool(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`UPDATE orgs SET settings = jsonb_set(settings, '{notifications}', '{"sender_name":"JJNE"}'::jsonb) WHERE id = $1`,
		orgID); err != nil {
		t.Fatalf("set sender name: %v", err)
	}

	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindCustom,
		DedupeKey: "custom:sender:1", Phone: "+255799000001", Body: "hello",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	parsed, _ := parseID(id)
	row, err := q.ClaimNotification(ctx, parsed)
	if err != nil {
		t.Fatalf("ClaimNotification: %v", err)
	}
	if row.SenderName != "JJNE" {
		t.Errorf("sender_name = %q, want JJNE", row.SenderName)
	}
}

// TestWorkerRetriesThenFails walks the backoff: three attempts against a
// provider that never answers, then a `failed` row carrying the reason.
func TestWorkerRetriesThenFails(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sms := &countingProvider{fail: true}
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindOverdueDaily,
		DedupeKey: "overdue_daily:retry:2026-09-05", Phone: "+255799000001", Body: "still outstanding",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), id)

	var slept []time.Duration
	var mu sync.Mutex
	go notify.RunWorker(ctx, notify.Worker{
		Q: q, Redis: redis.Client, SMS: sms, Logger: testutil.Logger(),
		PollWait: 50 * time.Millisecond, Workers: 1,
		Sleep: func(_ context.Context, d time.Duration) {
			mu.Lock()
			slept = append(slept, d)
			mu.Unlock()
		},
	})
	waitForStatus(t, pool, id, "failed")

	if got := sms.count(); got != notify.DefaultMaxAttempts {
		t.Errorf("the provider saw %d attempts, want %d", got, notify.DefaultMaxAttempts)
	}
	mu.Lock()
	got := append([]time.Duration(nil), slept...)
	mu.Unlock()
	want := notify.DefaultBackoff[:notify.DefaultMaxAttempts-1]
	if len(got) != len(want) {
		t.Fatalf("backoff pauses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("backoff pause %d = %v, want %v", i, got[i], want[i])
		}
	}

	var attempts int
	var reason *string
	if err := pool.QueryRow(context.Background(),
		`SELECT attempts, error FROM notification_log WHERE id = $1`, id).Scan(&attempts, &reason); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if attempts != notify.DefaultMaxAttempts {
		t.Errorf("attempts = %d, want %d", attempts, notify.DefaultMaxAttempts)
	}
	if reason == nil || *reason == "" {
		t.Error("a failed send recorded no reason")
	}
}

// TestWorkerRecoversFromATransientFailure: the second attempt succeeds, and the
// row records the number of tries it actually took.
func TestWorkerRecoversFromATransientFailure(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sms := &countingProvider{failFor: 1}
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindReminder7d,
		DedupeKey: "reminder_7d:flaky:2026-09-05", Phone: "+255799000001", Body: "due in a week",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), id)

	go notify.RunWorker(ctx, notify.Worker{
		Q: q, Redis: redis.Client, SMS: sms, Logger: testutil.Logger(),
		PollWait: 50 * time.Millisecond, Workers: 3, Sleep: noSleep,
	})
	waitForStatus(t, pool, id, "sent")

	var attempts int
	if err := pool.QueryRow(context.Background(),
		`SELECT attempts FROM notification_log WHERE id = $1`, id).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one failure, then success)", attempts)
	}
}

// TestPoolSendsEachMessageOnce: three workers, one push per message, and no
// renter is texted twice — the claim, not the queue, is what guarantees it.
func TestPoolSendsEachMessageOnce(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const messages = 5
	ids := make([]string, 0, messages)
	for i := 0; i < messages; i++ {
		id, err := notify.Queue(ctx, q, notify.Msg{
			OrgID: orgID, UserID: userID, Kind: notify.KindCustom,
			DedupeKey: "custom:pool:" + string(rune('a'+i)),
			Phone:     "+255799000001", Body: "notice",
		})
		if err != nil {
			t.Fatalf("Queue: %v", err)
		}
		ids = append(ids, id)
	}
	// Every id is pushed twice, the way a re-push or the recovery sweep would.
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), ids...)
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), ids...)

	sms := &countingProvider{}
	go notify.RunWorker(ctx, notify.Worker{
		Q: q, Redis: redis.Client, SMS: sms, Logger: testutil.Logger(),
		PollWait: 50 * time.Millisecond, Workers: 3, Sleep: noSleep,
	})
	for _, id := range ids {
		waitForStatus(t, pool, id, "sent")
	}
	waitForEmptyQueue(t, redis.Client)
	time.Sleep(200 * time.Millisecond) // let a wrong extra send land if it will

	if got := sms.count(); got != messages {
		t.Errorf("the provider saw %d sends for %d messages pushed twice each", got, messages)
	}
}

// TestRecoverQueuedReleasesStrandedSending is the crash case: a worker died
// holding a claim, so the row must come back rather than sit in `sending`.
func TestRecoverQueuedReleasesStrandedSending(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx := context.Background()

	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindReminderDue,
		DedupeKey: "reminder_due:stranded:2026-09-05", Phone: "+255799000001", Body: "due today",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	// The set_updated_at trigger would stamp `now()` over the backdated
	// timestamp, so it is switched off for this one statement — the point of
	// the test is a row whose claim is ten minutes old.
	if _, err := pool.Exec(ctx, `ALTER TABLE notification_log DISABLE TRIGGER notification_log_set_updated_at`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE notification_log
		SET status = 'sending', created_at = now() - interval '10 minutes',
		    updated_at = now() - interval '10 minutes'
		WHERE id = $1`, id); err != nil {
		t.Fatalf("strand the row: %v", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE notification_log ENABLE TRIGGER notification_log_set_updated_at`); err != nil {
		t.Fatalf("enable trigger: %v", err)
	}

	notify.RecoverQueued(ctx, q, redis.Client, testutil.Logger())

	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM notification_log WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "queued" {
		t.Fatalf("status after the sweep = %q, want queued", status)
	}
	items, err := redis.LRange(ctx, notify.QueueKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	if len(items) != 1 || items[0] != id {
		t.Errorf("queue holds %v, want just %q", items, id)
	}
}
