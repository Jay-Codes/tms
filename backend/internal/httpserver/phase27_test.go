package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tms/backend/internal/config"
	"tms/backend/internal/httpserver"
	"tms/backend/internal/snippe"
	"tms/backend/internal/testutil"
)

// Phase 27: landlords buy SMS credits with mobile money (Snippe). Every test
// runs against a fake Snippe (httptest); the real API is never called.

const (
	testSnippeKey    = "sk_test_phase27"
	testWebhookKey   = "whsec_test_phase27"
	testWebhookURL   = "https://tms.test/api/v1/webhooks/snippe"
	testStockBuffer  = 100
	testPackagePrice = 5_000
)

// --------------------------------------------------------- the fake --

type fakeCreate struct {
	Body           map[string]any
	IdempotencyKey string
	Auth           string
}

type fakePayment struct {
	Status string
	Amount int64
}

// fakeSnippe answers POST /v1/payments and GET /v1/payments/{ref} the way the
// Snippe docs describe.
type fakeSnippe struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	creates  []fakeCreate
	payments map[string]*fakePayment
	gets     int
	// refuse makes the next create answer this status with an error body.
	refuse int
}

func newFakeSnippe(t *testing.T) *fakeSnippe {
	t.Helper()
	f := &fakeSnippe{t: t, payments: map[string]*fakePayment{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSnippe) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+testSnippeKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error","code":401,"error_code":"unauthorized","message":"bad key"}`))
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/payments":
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.creates = append(f.creates, fakeCreate{
			Body: body, IdempotencyKey: r.Header.Get("Idempotency-Key"), Auth: r.Header.Get("Authorization"),
		})
		if f.refuse != 0 {
			code := f.refuse
			f.refuse = 0
			w.WriteHeader(code)
			_, _ = fmt.Fprintf(w, `{"status":"error","code":%d,"error_code":"invalid_phone","message":"The phone number is not registered for mobile money"}`, code)
			return
		}
		ref := fmt.Sprintf("SNP-REF-%d", len(f.creates))
		amount, _ := body["amount"].(float64)
		f.payments[ref] = &fakePayment{Status: "pending", Amount: int64(amount)}
		_, _ = fmt.Fprintf(w, `{"status":"success","code":200,"data":{"reference":%q,"id":"pay_%d","status":"pending"}}`,
			ref, len(f.creates))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/payments/"):
		f.gets++
		ref := strings.TrimPrefix(r.URL.Path, "/v1/payments/")
		p, ok := f.payments[ref]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","code":404,"error_code":"not_found","message":"no such payment"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","code":200,"data":{"reference":%q,"status":%q,"amount":{"value":%d,"currency":"TZS"}}}`,
			ref, p.Status, p.Amount)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeSnippe) setStatus(ref, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.payments[ref]; ok {
		p.Status = status
	}
}

func (f *fakeSnippe) lastCreate(t *testing.T) fakeCreate {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.creates) == 0 {
		t.Fatal("the fake Snippe received no payment request")
	}
	return f.creates[len(f.creates)-1]
}

func (f *fakeSnippe) getCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets
}

// ------------------------------------------------------- the harness --

// newSnippeHarness is newHarness with Snippe switched on and pointed at fake.
func newSnippeHarness(t *testing.T, fake *fakeSnippe) *harness {
	t.Helper()
	h := newHarness(t)
	cfg := config.Config{
		Env:                      config.EnvDev,
		Port:                     "0",
		AppBaseURL:               "http://localhost:8080",
		SessionTTLHours:          24,
		NidaEncKey:               "test-key",
		SnippeAPIKey:             testSnippeKey,
		SnippeWebhookSecret:      testWebhookKey,
		SnippeBaseURL:            fake.srv.URL,
		SnippeWebhookURLOverride: testWebhookURL,
		SMSStockBuffer:           testStockBuffer,
	}
	h.srv = httpserver.New(cfg, httpserver.Deps{
		DB: h.pool, Redis: h.redis, Pool: h.pool, Cache: h.redis, SMS: h.sms, Email: h.email, Storage: h.store,
	}, testutil.Logger())
	return h
}

// createPackage puts a bundle on sale through the admin route.
func createPackage(t *testing.T, admin *client, name string, credits int, price int64) string {
	t.Helper()
	return admin.do(http.MethodPost, "/admin/sms/packages", map[string]any{
		"name": name, "credits": credits, "price": price,
	}).mustStatus(t, http.StatusCreated, "create package "+name).str(t, "package", "id")
}

// placeOrder buys a package as owner and returns the order.
func placeOrder(t *testing.T, owner *client, packageID, phone string) map[string]any {
	t.Helper()
	body := map[string]any{"package_id": packageID}
	if phone != "" {
		body["phone"] = phone
	}
	resp := owner.do(http.MethodPost, "/org/sms-credits/orders", body).
		mustStatus(t, http.StatusCreated, "place order")
	order, ok := resp.Body["order"].(map[string]any)
	if !ok {
		t.Fatalf("order missing from response: %s", resp.Raw)
	}
	return order
}

// webhookEvent builds a Snippe webhook body.
func webhookEvent(id, typ, reference string, amount int64, orderCode string) []byte {
	data := map[string]any{
		"reference": reference,
		"amount":    map[string]any{"value": amount, "currency": "TZS"},
	}
	if orderCode != "" {
		data["metadata"] = map[string]any{"order_code": orderCode}
	}
	raw, _ := json.Marshal(map[string]any{
		"id": id, "type": typ, "api_version": "2026-01-25",
		"created_at": time.Now().UTC().Format(time.RFC3339), "data": data,
	})
	return raw
}

// postWebhook delivers raw to the webhook, signed with secret at ts.
func (h *harness) postWebhook(raw []byte, secret string, ts time.Time) response {
	h.t.Helper()
	stamp := strconv.FormatInt(ts.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, httpserver.APIPrefix+"/webhooks/snippe", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(snippe.HeaderEvent, "payment.completed")
	req.Header.Set(snippe.HeaderTimestamp, stamp)
	req.Header.Set(snippe.HeaderSignature, snippe.Sign(secret, stamp, raw))
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	out := response{Code: rec.Code, Raw: rec.Body.String(), Header: rec.Header()}
	_ = json.Unmarshal(rec.Body.Bytes(), &out.Body)
	return out
}

func (h *harness) orderStatus(t *testing.T, orderID string) string {
	t.Helper()
	var st string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT status FROM sms_credit_orders WHERE id = $1`, orderID).Scan(&st); err != nil {
		t.Fatalf("read order status: %v", err)
	}
	return st
}

// backdate moves an order's creation into the past, as the clock would.
func (h *harness) backdate(t *testing.T, orderID string, age time.Duration) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE sms_credit_orders SET created_at = now() - make_interval(secs => $2), last_checked_at = NULL
		 WHERE id = $1`, orderID, age.Seconds()); err != nil {
		t.Fatalf("backdate order: %v", err)
	}
}

// holdTwoMessages queues two custom SMS and parks them as held_no_credit,
// the way the worker would at a zero balance.
func (h *harness) holdTwoMessages(t *testing.T, fix creditFixture) {
	t.Helper()
	fix.owner.do(http.MethodPost, "/notifications/custom", map[string]any{
		"recipients": "all_active", "body_sw": "Habari", "body_en": "Hello",
	}).mustStatus(t, http.StatusAccepted, "bulk send")
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE notification_log SET status = 'held_no_credit'
		 WHERE org_id = $1 AND kind = 'custom'`, fix.orgID); err != nil {
		t.Fatalf("hold messages: %v", err)
	}
}

func (h *harness) heldCount(t *testing.T, orgID string) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM notification_log WHERE org_id = $1 AND status = 'held_no_credit'`,
		orgID).Scan(&n); err != nil {
		t.Fatalf("count held: %v", err)
	}
	return n
}

// ------------------------------------------------------------- tests --

// TestPhase27OrderSendsUSSDPush: the landlord picks a package, the API writes
// the order and asks Snippe for a push to the account phone, with the order
// code as the Idempotency-Key and the webhook URL.
func TestPhase27OrderSendsUSSDPush(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Buy", "0712270001", "+255714270002", "+255714270003")
	pkgID := createPackage(t, fix.admin, "Starter 100", 100, testPackagePrice)

	pkgs := fix.owner.do(http.MethodGet, "/org/sms-credits/packages", nil).
		mustStatus(t, http.StatusOK, "packages")
	if pkgs.Body["enabled"] != true {
		t.Errorf("enabled = %v, want true", pkgs.Body["enabled"])
	}
	if got := pkgs.str(t, "default_phone"); got != "+255712270001" {
		t.Errorf("default_phone = %q, want the account phone", got)
	}
	if items := arrayOf(t, pkgs, "items"); len(items) != 1 || items[0]["price"] != float64(testPackagePrice) {
		t.Fatalf("packages = %v", items)
	}

	order := placeOrder(t, fix.owner, pkgID, "")
	code, _ := order["order_code"].(string)
	if order["status"] != "pending" || order["credits"] != float64(100) ||
		order["amount"] != float64(testPackagePrice) || order["payer_phone"] != "+255712270001" {
		t.Errorf("order = %v", order)
	}
	if len(code) > snippe.MaxIdempotencyKey || !strings.HasPrefix(code, "SMS-") {
		t.Errorf("order_code %q must be ≤30 chars", code)
	}
	if order["reference"] != "SNP-REF-1" {
		t.Errorf("reference = %v, want the fake's SNP-REF-1", order["reference"])
	}

	sent := fake.lastCreate(t)
	if sent.IdempotencyKey != code {
		t.Errorf("Idempotency-Key = %q, want the order code %q", sent.IdempotencyKey, code)
	}
	if sent.Body["amount"] != float64(testPackagePrice) || sent.Body["currency"] != "TZS" ||
		sent.Body["phone"] != "255712270001" || sent.Body["payment_type"] != "mobile" ||
		sent.Body["webhook_url"] != testWebhookURL {
		t.Errorf("snippe request = %v", sent.Body)
	}
	if meta, _ := sent.Body["metadata"].(map[string]any); meta["order_code"] != code {
		t.Errorf("metadata = %v, want order_code", sent.Body["metadata"])
	}

	// Another phone may pay; it is normalised and stored on the order.
	other := placeOrder(t, fix.owner, pkgID, "0655 123 456")
	if other["payer_phone"] != "+255655123456" {
		t.Errorf("payer_phone = %v, want +255655123456", other["payer_phone"])
	}
	fix.owner.do(http.MethodPost, "/org/sms-credits/orders", map[string]any{
		"package_id": pkgID, "phone": "12345",
	}).mustStatus(t, http.StatusBadRequest, "not a Tanzanian mobile")

	list := fix.owner.do(http.MethodGet, "/org/sms-credits/orders", nil).mustStatus(t, http.StatusOK, "history")
	if items := arrayOf(t, list, "items"); len(items) != 2 {
		t.Errorf("history has %d orders, want 2", len(items))
	}
	one := fix.owner.do(http.MethodGet, "/org/sms-credits/orders/"+order["id"].(string), nil).
		mustStatus(t, http.StatusOK, "poll order")
	if one.str(t, "order", "status") != "pending" {
		t.Errorf("polled status = %s", one.str(t, "order", "status"))
	}
	// Nothing is credited by placing an order.
	if got := h.balance(t, fix.orgID); got != testStartingCredits {
		t.Errorf("balance = %d after ordering, want %d", got, testStartingCredits)
	}
}

// TestPhase27WebhookCreditsAndReleasesHeld: a signed payment.completed credits
// the org through the top-up path (ledger reason `purchase`) and releases the
// held messages; the same event again, or a second event for the same
// payment, credits nothing.
func TestPhase27WebhookCreditsAndReleasesHeld(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Hook", "0712270011", "+255714270012", "+255714270013")
	h.setBalance(t, fix.orgID, 5)
	h.holdTwoMessages(t, fix)
	h.setBalance(t, fix.orgID, 0)
	if h.heldCount(t, fix.orgID) != 2 {
		t.Fatal("fixture: two messages should be held")
	}

	pkgID := createPackage(t, fix.admin, "Hook 250", 250, testPackagePrice)
	order := placeOrder(t, fix.owner, pkgID, "")
	ref, _ := order["reference"].(string)

	raw := webhookEvent("evt_hook_1", snippe.EventCompleted, ref, testPackagePrice, "")
	resp := h.postWebhook(raw, testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "webhook")
	if resp.Body["outcome"] != "completed" {
		t.Fatalf("outcome = %v, want completed", resp.Body["outcome"])
	}
	if got := h.balance(t, fix.orgID); got != 250 {
		t.Errorf("balance = %d, want 250", got)
	}
	if got := h.heldCount(t, fix.orgID); got != 0 {
		t.Errorf("%d messages still held after the purchase, want 0", got)
	}
	if got := h.orderStatus(t, order["id"].(string)); got != "completed" {
		t.Errorf("order status = %s", got)
	}
	var reason string
	var delta int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT reason, delta FROM sms_credit_ledger WHERE org_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		fix.orgID).Scan(&reason, &delta); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if reason != "purchase" || delta != 250 {
		t.Errorf("ledger row = %s %d, want purchase 250", reason, delta)
	}

	// Snippe retries: the same event id is a no-op.
	again := h.postWebhook(raw, testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "retried webhook")
	if again.Body["outcome"] != "duplicate" {
		t.Errorf("retry outcome = %v, want duplicate", again.Body["outcome"])
	}
	// A different event about the same payment credits nothing either.
	second := h.postWebhook(webhookEvent("evt_hook_2", snippe.EventCompleted, ref, testPackagePrice, ""),
		testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "second event")
	if second.Body["outcome"] != "already_completed" {
		t.Errorf("second outcome = %v, want already_completed", second.Body["outcome"])
	}
	if got := h.balance(t, fix.orgID); got != 250 {
		t.Errorf("balance = %d after replays, want 250", got)
	}

	// The landlord's poll shows the order done.
	polled := fix.owner.do(http.MethodGet, "/org/sms-credits/orders/"+order["id"].(string), nil).
		mustStatus(t, http.StatusOK, "poll")
	if polled.str(t, "order", "status") != "completed" {
		t.Errorf("polled status = %s", polled.str(t, "order", "status"))
	}
}

// TestPhase27WebhookFindsOrderByCode: an order whose reference was never
// stored (the create call timed out) is still matched by the order code in
// the metadata.
func TestPhase27WebhookFindsOrderByCode(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Code", "0712270021", "+255714270022", "+255714270023")
	h.setBalance(t, fix.orgID, 0)
	pkgID := createPackage(t, fix.admin, "Code 100", 100, testPackagePrice)
	order := placeOrder(t, fix.owner, pkgID, "")
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE sms_credit_orders SET snippe_reference = NULL WHERE id = $1`, order["id"]); err != nil {
		t.Fatal(err)
	}
	resp := h.postWebhook(webhookEvent("evt_code_1", snippe.EventCompleted, "SNP-LATE", testPackagePrice,
		order["order_code"].(string)), testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "webhook")
	if resp.Body["outcome"] != "completed" || h.balance(t, fix.orgID) != 100 {
		t.Errorf("outcome %v, balance %d; want completed, 100", resp.Body["outcome"], h.balance(t, fix.orgID))
	}

	// An event nobody ordered is recorded and answered 2xx, not credited.
	unknown := h.postWebhook(webhookEvent("evt_code_2", snippe.EventCompleted, "SNP-NOBODY", 5000, ""),
		testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "unknown order")
	if unknown.Body["outcome"] != "unknown_order" {
		t.Errorf("outcome = %v, want unknown_order", unknown.Body["outcome"])
	}
}

// TestPhase27WebhookSignature: a wrong key or a stale timestamp is refused
// before anything is read, and nothing is recorded or credited.
func TestPhase27WebhookSignature(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Sig", "0712270031", "+255714270032", "+255714270033")
	h.setBalance(t, fix.orgID, 0)
	pkgID := createPackage(t, fix.admin, "Sig 100", 100, testPackagePrice)
	order := placeOrder(t, fix.owner, pkgID, "")
	raw := webhookEvent("evt_sig_1", snippe.EventCompleted, order["reference"].(string), testPackagePrice, "")

	bad := h.postWebhook(raw, "not-the-key", time.Now()).mustStatus(t, http.StatusUnauthorized, "bad signature")
	if bad.str(t, "type") != "invalid_signature" {
		t.Errorf("type = %s, want invalid_signature", bad.str(t, "type"))
	}
	stale := h.postWebhook(raw, testWebhookKey, time.Now().Add(-6*time.Minute)).
		mustStatus(t, http.StatusUnauthorized, "stale timestamp")
	if stale.str(t, "type") != "stale_webhook" {
		t.Errorf("type = %s, want stale_webhook", stale.str(t, "type"))
	}
	// Tampering with a correctly signed body breaks the signature.
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := snippe.Sign(testWebhookKey, stamp, raw)
	tampered := bytes.Replace(raw, []byte(`"value":5000`), []byte(`"value":5`), 1)
	req := httptest.NewRequest(http.MethodPost, httpserver.APIPrefix+"/webhooks/snippe", bytes.NewReader(tampered))
	req.Header.Set(snippe.HeaderTimestamp, stamp)
	req.Header.Set(snippe.HeaderSignature, sig)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered body: status %d, want 401", rec.Code)
	}

	var events int
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM snippe_webhook_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 || h.balance(t, fix.orgID) != 0 || h.orderStatus(t, order["id"].(string)) != "pending" {
		t.Errorf("after refused deliveries: %d events, balance %d, status %s — want 0, 0, pending",
			events, h.balance(t, fix.orgID), h.orderStatus(t, order["id"].(string)))
	}
}

// TestPhase27WebhookAmountMismatch: a completed payment for another amount is
// not credited; the order is set aside for an admin.
func TestPhase27WebhookAmountMismatch(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Mis", "0712270041", "+255714270042", "+255714270043")
	h.setBalance(t, fix.orgID, 0)
	pkgID := createPackage(t, fix.admin, "Mis 100", 100, testPackagePrice)
	order := placeOrder(t, fix.owner, pkgID, "")

	resp := h.postWebhook(webhookEvent("evt_mis_1", snippe.EventCompleted, order["reference"].(string), 500, ""),
		testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "mismatched webhook")
	if resp.Body["outcome"] != "amount_mismatch" {
		t.Errorf("outcome = %v, want amount_mismatch", resp.Body["outcome"])
	}
	if got := h.orderStatus(t, order["id"].(string)); got != "mismatch" {
		t.Errorf("order status = %s, want mismatch", got)
	}
	if got := h.balance(t, fix.orgID); got != 0 {
		t.Errorf("balance = %d, want 0", got)
	}
	orders := fix.admin.do(http.MethodGet, "/admin/sms/orders?status=mismatch", nil).
		mustStatus(t, http.StatusOK, "admin mismatch list")
	if items := arrayOf(t, orders, "items"); len(items) != 1 || items[0]["org_name"] == "" {
		t.Errorf("admin mismatch list = %v", items)
	}
}

// TestPhase27WebhookFailedAndExpired: the non-paying outcomes close the order
// and credit nothing; a late completion after the local expiry still credits
// (the money was taken).
func TestPhase27WebhookFailedAndExpired(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Fail", "0712270051", "+255714270052", "+255714270053")
	h.setBalance(t, fix.orgID, 0)
	pkgID := createPackage(t, fix.admin, "Fail 100", 100, testPackagePrice)

	failed := placeOrder(t, fix.owner, pkgID, "")
	h.postWebhook(webhookEvent("evt_fail_1", snippe.EventFailed, failed["reference"].(string), testPackagePrice, ""),
		testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "failed webhook")
	if got := h.orderStatus(t, failed["id"].(string)); got != "failed" {
		t.Errorf("status = %s, want failed", got)
	}

	late := placeOrder(t, fix.owner, pkgID, "")
	h.postWebhook(webhookEvent("evt_exp_1", snippe.EventExpired, late["reference"].(string), testPackagePrice, ""),
		testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "expired webhook")
	if got := h.orderStatus(t, late["id"].(string)); got != "expired" {
		t.Errorf("status = %s, want expired", got)
	}
	h.postWebhook(webhookEvent("evt_late_1", snippe.EventCompleted, late["reference"].(string), testPackagePrice, ""),
		testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "late completion")
	if got := h.balance(t, fix.orgID); got != 100 {
		t.Errorf("balance = %d after a late completion, want 100", got)
	}
}

// TestPhase27ReconciliationCompletesAndExpires: orders the webhook never
// settled are polled once five minutes old; a completed one is credited, a
// failed one closed, and one still pending after four hours expired.
func TestPhase27ReconciliationCompletesAndExpires(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Rec", "0712270061", "+255714270062", "+255714270063")
	h.setBalance(t, fix.orgID, 0)
	h.setBalance(t, fix.orgID, 5)
	h.holdTwoMessages(t, fix)
	h.setBalance(t, fix.orgID, 0)
	pkgID := createPackage(t, fix.admin, "Rec 300", 300, testPackagePrice)

	paid := placeOrder(t, fix.owner, pkgID, "")
	failed := placeOrder(t, fix.owner, pkgID, "")
	stuck := placeOrder(t, fix.owner, pkgID, "")
	young := placeOrder(t, fix.owner, pkgID, "")
	fake.setStatus(paid["reference"].(string), "completed")
	fake.setStatus(failed["reference"].(string), "failed")
	fake.setStatus(young["reference"].(string), "completed")
	h.backdate(t, paid["id"].(string), 10*time.Minute)
	h.backdate(t, failed["id"].(string), 10*time.Minute)
	h.backdate(t, stuck["id"].(string), 5*time.Hour)

	res := fix.admin.do(http.MethodPost, "/admin/sms/orders/reconcile", nil).
		mustStatus(t, http.StatusOK, "reconcile")
	if num(t, res, "checked") != 3 || num(t, res, "completed") != 1 ||
		num(t, res, "failed") != 1 || num(t, res, "expired") != 1 {
		t.Errorf("reconcile = %s", res.Raw)
	}
	if got := h.orderStatus(t, paid["id"].(string)); got != "completed" {
		t.Errorf("paid order = %s", got)
	}
	if got := h.orderStatus(t, failed["id"].(string)); got != "failed" {
		t.Errorf("failed order = %s", got)
	}
	if got := h.orderStatus(t, stuck["id"].(string)); got != "expired" {
		t.Errorf("stuck order = %s", got)
	}
	// Under five minutes old: left to the webhook.
	if got := h.orderStatus(t, young["id"].(string)); got != "pending" {
		t.Errorf("young order = %s, want pending", got)
	}
	if got := h.balance(t, fix.orgID); got != 300 {
		t.Errorf("balance = %d, want 300", got)
	}
	if got := h.heldCount(t, fix.orgID); got != 0 {
		t.Errorf("%d messages still held after the reconciled purchase", got)
	}

	// A second sweep straight away asks about nothing already settled.
	again := fix.admin.do(http.MethodPost, "/admin/sms/orders/reconcile", nil).mustStatus(t, http.StatusOK, "again")
	if num(t, again, "checked") != 0 {
		t.Errorf("second sweep checked %v orders, want 0", num(t, again, "checked"))
	}
	if got := h.balance(t, fix.orgID); got != 300 {
		t.Errorf("balance = %d after a second sweep, want 300", got)
	}
}

// TestPhase27PollAsksSnippe: the waiting screen's poll settles an order
// without the webhook once it is a little old.
func TestPhase27PollAsksSnippe(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Poll", "0712270071", "+255714270072", "+255714270073")
	h.setBalance(t, fix.orgID, 0)
	pkgID := createPackage(t, fix.admin, "Poll 100", 100, testPackagePrice)
	order := placeOrder(t, fix.owner, pkgID, "")
	id := order["id"].(string)
	fake.setStatus(order["reference"].(string), "completed")

	fix.owner.do(http.MethodGet, "/org/sms-credits/orders/"+id, nil).mustStatus(t, http.StatusOK, "fresh poll")
	if fake.getCount() != 0 {
		t.Errorf("a brand-new order asked Snippe %d times, want 0", fake.getCount())
	}
	h.backdate(t, id, time.Minute)
	polled := fix.owner.do(http.MethodGet, "/org/sms-credits/orders/"+id, nil).mustStatus(t, http.StatusOK, "poll")
	if polled.str(t, "order", "status") != "completed" || h.balance(t, fix.orgID) != 100 {
		t.Errorf("after poll: status %s, balance %d", polled.str(t, "order", "status"), h.balance(t, fix.orgID))
	}
}

// TestPhase27RefusedByProvider: Snippe refusing the push closes the order and
// tells the landlord why.
func TestPhase27RefusedByProvider(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Ref", "0712270081", "+255714270082", "+255714270083")
	pkgID := createPackage(t, fix.admin, "Ref 100", 100, testPackagePrice)
	fake.refuse = http.StatusBadRequest

	resp := fix.owner.do(http.MethodPost, "/org/sms-credits/orders", map[string]any{"package_id": pkgID}).
		mustStatus(t, http.StatusBadGateway, "refused")
	if resp.str(t, "type") != "payment_request_refused" {
		t.Errorf("type = %s", resp.str(t, "type"))
	}
	list := fix.owner.do(http.MethodGet, "/org/sms-credits/orders", nil).mustStatus(t, http.StatusOK, "history")
	items := arrayOf(t, list, "items")
	if len(items) != 1 || items[0]["status"] != "failed" || items[0]["failure_reason"] == nil {
		t.Errorf("history = %v, want one failed order with a reason", items)
	}
}

// TestPhase27PurchasesDisabled: with no Snippe keys the purchase routes answer
// 503 `purchases_disabled`, and everything else still works.
func TestPhase27PurchasesDisabled(t *testing.T) {
	h := newHarness(t)
	fix := h.newCreditFixture(t, "Off", "0712270091", "+255714270092", "+255714270093")
	pkgID := createPackage(t, fix.admin, "Off 100", 100, testPackagePrice)

	pkgs := fix.owner.do(http.MethodGet, "/org/sms-credits/packages", nil).mustStatus(t, http.StatusOK, "packages")
	if pkgs.Body["enabled"] != false {
		t.Errorf("enabled = %v, want false", pkgs.Body["enabled"])
	}
	resp := fix.owner.do(http.MethodPost, "/org/sms-credits/orders", map[string]any{"package_id": pkgID}).
		mustStatus(t, http.StatusServiceUnavailable, "disabled purchase")
	if resp.str(t, "type") != "purchases_disabled" {
		t.Errorf("type = %s", resp.str(t, "type"))
	}
	hook := h.postWebhook(webhookEvent("evt_off", snippe.EventCompleted, "r", 5000, ""), "x", time.Now()).
		mustStatus(t, http.StatusServiceUnavailable, "disabled webhook")
	if hook.str(t, "type") != "purchases_disabled" {
		t.Errorf("webhook type = %s", hook.str(t, "type"))
	}
	fix.owner.do(http.MethodGet, "/org/sms-credits/orders", nil).mustStatus(t, http.StatusOK, "history still reads")
	fix.owner.do(http.MethodGet, "/org/sms-credits", nil).mustStatus(t, http.StatusOK, "balance still reads")
	fix.admin.do(http.MethodGet, "/admin/sms/stock", nil).mustStatus(t, http.StatusOK, "stock still reads")
}

// TestPhase27Isolation: org B never sees org A's orders, and a renter cannot
// reach the org routes at all.
func TestPhase27Isolation(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	a := h.newCreditFixture(t, "IsoA", "0712270101", "+255714270102", "+255714270103")
	pkgID := createPackage(t, a.admin, "Iso 100", 100, testPackagePrice)
	order := placeOrder(t, a.owner, pkgID, "")

	ownerB, _ := h.createOrg("P27 Beta", "Bea Beta", "p27-beta@jjne.test", "0712270110", "supersecret")
	ownerB.do(http.MethodGet, "/org/sms-credits/orders/"+order["id"].(string), nil).
		mustStatus(t, http.StatusNotFound, "org B reading org A's order")
	list := ownerB.do(http.MethodGet, "/org/sms-credits/orders", nil).mustStatus(t, http.StatusOK, "org B history")
	if strings.Contains(list.Raw, order["id"].(string)) || strings.Contains(list.Raw, order["order_code"].(string)) {
		t.Errorf("org B's history carries org A's order: %s", list.Raw)
	}
	renter := h.registerRenter("+255714270111", "P27 Renter", defaultPIN)
	renter.do(http.MethodGet, "/org/sms-credits/orders", nil).mustStatus(t, http.StatusUnauthorized, "renter")
	renter.do(http.MethodPost, "/org/sms-credits/orders", map[string]any{"package_id": pkgID}).
		mustStatus(t, http.StatusUnauthorized, "renter buying")
	a.owner.do(http.MethodGet, "/admin/sms/orders", nil).mustStatus(t, http.StatusUnauthorized, "owner on admin")
}

// TestPhase27AdminPackagesStockAndMargin: packages are validated and edited
// in place (orders keep their copy); stock is bought − sent against what orgs
// hold; the margin is sales − Snippe's 2.5% − the Beem cost of the credits.
func TestPhase27AdminPackagesStockAndMargin(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Stock", "0712270121", "+255714270122", "+255714270123")
	admin := fix.admin

	admin.do(http.MethodPost, "/admin/sms/packages", map[string]any{"name": "Cheap", "credits": 10, "price": 499}).
		mustStatus(t, http.StatusBadRequest, "under Snippe's minimum")
	admin.do(http.MethodPost, "/admin/sms/packages", map[string]any{"name": "", "credits": 0, "price": 1000}).
		mustStatus(t, http.StatusBadRequest, "no name, no credits")
	pkgID := createPackage(t, admin, "Stock 100", 100, 10_000)

	order := placeOrder(t, fix.owner, pkgID, "")
	// A price change after the order does not move what the order was for.
	admin.do(http.MethodPatch, "/admin/sms/packages/"+pkgID, map[string]any{"price": 12_000}).
		mustStatus(t, http.StatusOK, "reprice")
	h.postWebhook(webhookEvent("evt_stock_1", snippe.EventCompleted, order["reference"].(string), 10_000, ""),
		testWebhookKey, time.Now()).mustStatus(t, http.StatusOK, "webhook")
	if got := h.orderStatus(t, order["id"].(string)); got != "completed" {
		t.Fatalf("order = %s, want completed (credited at the price it was placed at)", got)
	}
	admin.do(http.MethodPatch, "/admin/sms/packages/"+pkgID, map[string]any{"active": false}).
		mustStatus(t, http.StatusOK, "take off sale")
	pkgs := fix.owner.do(http.MethodGet, "/org/sms-credits/packages", nil).mustStatus(t, http.StatusOK, "on sale")
	if items := arrayOf(t, pkgs, "items"); len(items) != 0 {
		t.Errorf("an inactive package is still on sale: %v", items)
	}
	fix.owner.do(http.MethodPost, "/org/sms-credits/orders", map[string]any{"package_id": pkgID}).
		mustStatus(t, http.StatusBadRequest, "buying an inactive package")

	admin.do(http.MethodPost, "/admin/sms/purchases", map[string]any{"sms_count": 50_000, "cost": 1_000_000,
		"purchased_on": "2026-09-01", "reference": "BEEM-INV-1"}).
		mustStatus(t, http.StatusCreated, "record beem bundle")
	admin.do(http.MethodPost, "/admin/sms/purchases", map[string]any{"sms_count": 10}).
		mustStatus(t, http.StatusBadRequest, "cost is required")
	purchases := admin.do(http.MethodGet, "/admin/sms/purchases", nil).mustStatus(t, http.StatusOK, "purchases")
	if items := arrayOf(t, purchases, "items"); len(items) != 1 || items[0]["purchased_on"] != "2026-09-01" {
		t.Errorf("purchases = %v", items)
	}

	var liability int64
	if err := h.pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(balance), 0) FROM org_sms_credits`).Scan(&liability); err != nil {
		t.Fatal(err)
	}
	stock := admin.do(http.MethodGet, "/admin/sms/stock", nil).mustStatus(t, http.StatusOK, "stock")
	if num(t, stock, "sms_bought") != 50_000 || num(t, stock, "liability") != float64(liability) ||
		num(t, stock, "avg_cost_per_sms") != 20 || num(t, stock, "buffer") != testStockBuffer ||
		num(t, stock, "credits_purchased") != 100 {
		t.Errorf("stock = %s", stock.Raw)
	}
	wantLow := num(t, stock, "stock") < float64(liability+testStockBuffer)
	if stock.Body["low_stock"] != wantLow {
		t.Errorf("low_stock = %v, want %v (stock %v, liability %d)", stock.Body["low_stock"], wantLow,
			num(t, stock, "stock"), liability)
	}

	today := time.Now().UTC().Format("2006-01-02")
	margin := admin.do(http.MethodGet, "/admin/sms/margin?from="+today+"&to="+today, nil).
		mustStatus(t, http.StatusOK, "margin")
	// 10 000 sales − 250 Snippe fee − 100 credits × TZS 20 = 7 750.
	if num(t, margin, "sales") != 10_000 || num(t, margin, "snippe_fee") != 250 ||
		num(t, margin, "beem_cost") != 2_000 || num(t, margin, "margin") != 7_750 {
		t.Errorf("margin = %s", margin.Raw)
	}
	admin.do(http.MethodGet, "/admin/sms/margin?from=2026-09-10&to=2026-09-01", nil).
		mustStatus(t, http.StatusBadRequest, "to before from")
}

// TestPhase27OrderRateLimit: a sixth prompt within ten minutes is refused.
func TestPhase27OrderRateLimit(t *testing.T) {
	fake := newFakeSnippe(t)
	h := newSnippeHarness(t, fake)
	fix := h.newCreditFixture(t, "Rate", "0712270131", "+255714270132", "+255714270133")
	pkgID := createPackage(t, fix.admin, "Rate 100", 100, testPackagePrice)
	for i := 0; i < 5; i++ {
		placeOrder(t, fix.owner, pkgID, "")
	}
	resp := fix.owner.do(http.MethodPost, "/org/sms-credits/orders", map[string]any{"package_id": pkgID}).
		mustStatus(t, http.StatusTooManyRequests, "sixth order")
	if resp.str(t, "type") != "too_many_orders" {
		t.Errorf("type = %s", resp.str(t, "type"))
	}
}
