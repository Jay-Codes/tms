package notify_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tms/backend/internal/db"
	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/notify"
	"tms/backend/internal/testutil"
)

// seedOrgAndRenter creates the minimum rows a notification needs to exist:
// notification_log carries an org and a user foreign key.
func seedOrgAndRenter(t *testing.T, q *sqlc.Queries) (orgID, userID string) {
	t.Helper()
	ctx := context.Background()

	org, err := q.CreateOrg(ctx, sqlc.CreateOrgParams{
		Name: "Queue Ltd", Slug: "queue-ltd", Settings: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	phone := "+255799000001"
	user, err := q.CreateUser(ctx, sqlc.CreateUserParams{
		Kind: "renter", Phone: &phone, FullName: "Queue Renter",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return db.UUIDString(org.ID), db.UUIDString(user.ID)
}

// TestQueueDedupes: the dedupe key is what stops a retried approval from
// texting the renter twice (SPEC §6).
func TestQueueDedupes(t *testing.T) {
	pool := testutil.Pool(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx := context.Background()

	msg := notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindLinkApproved,
		DedupeKey: "link_approved:abc", Phone: "+255799000001", Body: "approved",
	}
	id, err := notify.Queue(ctx, q, msg)
	if err != nil {
		t.Fatalf("first Queue: %v", err)
	}
	if id == "" {
		t.Fatal("first Queue returned an empty id")
	}

	second, err := notify.Queue(ctx, q, msg)
	if !errors.Is(err, notify.ErrDuplicate) {
		t.Fatalf("second Queue error = %v, want ErrDuplicate", err)
	}
	if second != "" {
		t.Errorf("second Queue returned id %q, want empty", second)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notification_log WHERE dedupe_key = 'link_approved:abc'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("notification_log holds %d rows for one dedupe key, want 1", count)
	}
}

// TestWorkerSendsAndRecordsOutcome walks the queue end to end: a row written by
// Queue, pushed by Enqueue, popped by the worker, sent, and marked `sent`.
func TestWorkerSendsAndRecordsOutcome(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sms := testutil.NewSMSCapture()
	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindLinkApproved,
		DedupeKey: "link_approved:sent", Phone: "+255799000001",
		Body: "Your request for Room 1 at Queue Ltd was approved.",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), id)

	go notify.RunWorker(ctx, notify.Worker{
		Q: q, Redis: redis.Client, SMS: sms, Logger: testutil.Logger(),
		PollWait: 50 * time.Millisecond,
	})

	waitForStatus(t, pool, id, "sent")

	msgs := sms.Messages()
	if len(msgs) != 1 {
		t.Fatalf("the provider saw %d messages, want 1", len(msgs))
	}
	if msgs[0].To != "+255799000001" {
		t.Errorf("sent to %q, want the renter's number", msgs[0].To)
	}
	if !strings.Contains(msgs[0].Body, "Room 1") {
		t.Errorf("body %q does not name the unit", msgs[0].Body)
	}

	var providerID *string
	if err := pool.QueryRow(ctx,
		`SELECT provider_msg_id FROM notification_log WHERE id = $1`, id).Scan(&providerID); err != nil {
		t.Fatalf("read provider_msg_id: %v", err)
	}
	if providerID == nil || *providerID == "" {
		t.Error("provider_msg_id was not recorded on a successful send")
	}
}

// TestWorkerRecordsFailure: a provider error must leave a `failed` row with the
// reason, not a silently lost message.
func TestWorkerRecordsFailure(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindLinkRejected,
		DedupeKey: "link_rejected:fail", Phone: "+255799000001", Body: "rejected",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	notify.Enqueue(ctx, redis.Client, testutil.Logger(), id)

	go notify.RunWorker(ctx, notify.Worker{
		Q: q, Redis: redis.Client, SMS: notify.DisabledSMSProvider{},
		Logger: testutil.Logger(), PollWait: 50 * time.Millisecond,
	})

	waitForStatus(t, pool, id, "failed")

	var reason *string
	if err := pool.QueryRow(ctx,
		`SELECT error FROM notification_log WHERE id = $1`, id).Scan(&reason); err != nil {
		t.Fatalf("read error column: %v", err)
	}
	if reason == nil || *reason == "" {
		t.Error("a failed send recorded no reason")
	}
}

// TestRecoverQueuedRepushesLostWork is the Redis-loss safety net: rows written
// before the cache was cleared must find their way back onto the list.
func TestRecoverQueuedRepushesLostWork(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx := context.Background()

	id, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindLinkApproved,
		DedupeKey: "link_approved:lost", Phone: "+255799000001", Body: "approved",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	// Simulate the loss: the row exists, Redis never heard about it, and it is
	// older than the staleness window.
	if _, err := pool.Exec(ctx,
		`UPDATE notification_log SET created_at = now() - interval '10 minutes' WHERE id = $1`, id); err != nil {
		t.Fatalf("age the row: %v", err)
	}
	if n, err := redis.LLen(ctx, notify.QueueKey).Result(); err != nil || n != 0 {
		t.Fatalf("queue length = %d (err %v), want an empty queue", n, err)
	}

	notify.RecoverQueued(ctx, q, redis.Client, testutil.Logger())

	items, err := redis.LRange(ctx, notify.QueueKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	if len(items) != 1 || items[0] != id {
		t.Fatalf("queue holds %v, want just %q", items, id)
	}
}

// TestRecoverQueuedIgnoresFreshRows: a message queued a moment ago is still in
// flight and must not be pushed a second time.
func TestRecoverQueuedIgnoresFreshRows(t *testing.T) {
	pool := testutil.Pool(t)
	redis := testutil.Redis(t)
	q := sqlc.New(pool)
	orgID, userID := seedOrgAndRenter(t, q)
	ctx := context.Background()

	if _, err := notify.Queue(ctx, q, notify.Msg{
		OrgID: orgID, UserID: userID, Kind: notify.KindLinkApproved,
		DedupeKey: "link_approved:fresh", Phone: "+255799000001", Body: "approved",
	}); err != nil {
		t.Fatalf("Queue: %v", err)
	}

	notify.RecoverQueued(ctx, q, redis.Client, testutil.Logger())

	if n, err := redis.LLen(ctx, notify.QueueKey).Result(); err != nil || n != 0 {
		t.Fatalf("queue length = %d (err %v), want 0 — a fresh row is not stale", n, err)
	}
}

// TestRenderLink pins both templates in both languages.
func TestRenderLink(t *testing.T) {
	vars := notify.LinkVars{Unit: "Room 1", Org: "JJnE Rentals", Reason: "unit already promised"}

	cases := []struct {
		kind, lang string
		want       []string
	}{
		{notify.KindLinkApproved, notify.LangEnglish,
			[]string{"Room 1", "JJnE Rentals", "was approved", "ready to sign"}},
		{notify.KindLinkApproved, notify.LangSwahili,
			[]string{"Room 1", "JJnE Rentals", "limekubaliwa"}},
		{notify.KindLinkRejected, notify.LangEnglish,
			[]string{"Room 1", "JJnE Rentals", "was not approved", "unit already promised"}},
		{notify.KindLinkRejected, notify.LangSwahili,
			[]string{"Room 1", "JJnE Rentals", "halikukubaliwa", "unit already promised"}},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.lang, func(t *testing.T) {
			body := notify.RenderLink(tc.kind, tc.lang, vars)
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("body %q does not contain %q", body, want)
				}
			}
			if strings.Contains(body, "{") {
				t.Errorf("body %q still holds an unsubstituted placeholder", body)
			}
		})
	}

	t.Run("an unknown language falls back to Swahili", func(t *testing.T) {
		body := notify.RenderLink(notify.KindLinkApproved, "fr", vars)
		if !strings.Contains(body, "limekubaliwa") {
			t.Errorf("body %q, want the Swahili fallback", body)
		}
	})

	t.Run("an unknown kind renders nothing", func(t *testing.T) {
		if body := notify.RenderLink("nonsense", notify.LangEnglish, vars); body != "" {
			t.Errorf("body = %q, want empty", body)
		}
	})
}

// waitForStatus polls the row until the worker has recorded an outcome.
func waitForStatus(t *testing.T, pool *db.Pool, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT status FROM notification_log WHERE id = $1`, id).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("notification %s has status %q, want %q", id, got, want)
}
