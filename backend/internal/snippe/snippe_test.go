package snippe_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"tms/backend/internal/snippe"
)

func TestVerifySignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"id":"evt_1","type":"payment.completed"}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := snippe.Sign("whsec", ts, body)

	if err := snippe.Verify("whsec", ts, sig, body, now); err != nil {
		t.Fatalf("good signature refused: %v", err)
	}
	if err := snippe.Verify("whsec", ts, "sha256="+sig, body, now); err != nil {
		t.Fatalf("sha256= prefixed signature refused: %v", err)
	}
	if err := snippe.Verify("other", ts, sig, body, now); !errors.Is(err, snippe.ErrBadSignature) {
		t.Errorf("wrong key: err = %v, want ErrBadSignature", err)
	}
	if err := snippe.Verify("whsec", ts, sig, append(body, ' '), now); !errors.Is(err, snippe.ErrBadSignature) {
		t.Errorf("altered body: err = %v, want ErrBadSignature", err)
	}
	if err := snippe.Verify("whsec", ts, "zz", body, now); !errors.Is(err, snippe.ErrBadSignature) {
		t.Errorf("non-hex signature: err = %v, want ErrBadSignature", err)
	}
	if err := snippe.Verify("whsec", ts, sig, body, now.Add(6*time.Minute)); !errors.Is(err, snippe.ErrStale) {
		t.Errorf("six-minute-old delivery: err = %v, want ErrStale", err)
	}
	if err := snippe.Verify("whsec", ts, sig, body, now.Add(-6*time.Minute)); !errors.Is(err, snippe.ErrStale) {
		t.Errorf("delivery from the future: err = %v, want ErrStale", err)
	}
	if err := snippe.Verify("", ts, sig, body, now); !errors.Is(err, snippe.ErrBadSignature) {
		t.Errorf("empty secret: err = %v, want ErrBadSignature", err)
	}
}

func TestAmountDecodesBothShapes(t *testing.T) {
	for _, raw := range []string{`{"value":5000,"currency":"TZS"}`, `5000`, `"5000"`, `{"value":"5000","currency":"TZS"}`} {
		var a snippe.Amount
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if a.Value != 5000 {
			t.Errorf("%s: value = %d, want 5000", raw, a.Value)
		}
	}
}

func TestFee(t *testing.T) {
	cases := map[int64]int64{1000: 25, 10_000: 250, 500: 13, 0: 0}
	for amount, want := range cases {
		if got := snippe.Fee(amount); got != want {
			t.Errorf("Fee(%d) = %d, want %d", amount, got, want)
		}
	}
}

func TestCreatePaymentAgainstFake(t *testing.T) {
	var gotBody map[string]any
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/payments" {
			http.NotFound(w, r)
			return
		}
		gotKey = r.Header.Get("Idempotency-Key")
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"status":"success","code":200,"data":{"reference":"ref_1","id":"pay_1","status":"pending"}}`))
	}))
	defer srv.Close()

	c := snippe.New("sk_test", srv.URL)
	p, err := c.CreatePayment(context.Background(), snippe.CreateRequest{
		Amount: 5000, Phone: "+255712000001", IdempotencyKey: "SMS-ABCDEFGH23",
		Metadata: map[string]string{"order_code": "SMS-ABCDEFGH23"}, WebhookURL: "https://x/api/v1/webhooks/snippe",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Reference != "ref_1" || p.Status != "pending" {
		t.Errorf("payment = %+v", p)
	}
	if gotKey != "SMS-ABCDEFGH23" || gotAuth != "Bearer sk_test" {
		t.Errorf("headers: key %q auth %q", gotKey, gotAuth)
	}
	if gotBody["phone"] != "255712000001" || gotBody["currency"] != "TZS" ||
		gotBody["payment_type"] != "mobile" || gotBody["amount"] != float64(5000) {
		t.Errorf("body = %v", gotBody)
	}
}

func TestCreatePaymentRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","code":400,"error_code":"invalid_phone","message":"phone is not a mobile number"}`))
	}))
	defer srv.Close()
	_, err := snippe.New("sk", srv.URL).CreatePayment(context.Background(), snippe.CreateRequest{
		Amount: 5000, Phone: "+255712000001", IdempotencyKey: "SMS-ABCDEFGH23",
	})
	var se *snippe.Error
	if !errors.As(err, &se) || se.Code != "invalid_phone" || se.HTTPStatus != 400 {
		t.Fatalf("err = %v, want a snippe.Error invalid_phone", err)
	}
}

func TestNotConfigured(t *testing.T) {
	if _, err := snippe.New("", "https://api.snippe.sh").GetPayment(context.Background(), "r"); !errors.Is(err, snippe.ErrNotConfigured) {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}
