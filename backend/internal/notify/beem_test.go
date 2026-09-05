package notify_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tms/backend/internal/config"
	"tms/backend/internal/notify"
)

// beemServer stands in for Beem: it records the one request it is sent and
// answers with whatever the test asked for.
type beemServer struct {
	srv    *httptest.Server
	gotReq map[string]any
	gotHdr http.Header
}

func newBeemServer(t *testing.T, status int, response string) *beemServer {
	t.Helper()
	b := &beemServer{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &b.gotReq)
		b.gotHdr = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func beemProvider(b *beemServer, senderID string) *notify.BeemProvider {
	p := notify.NewBeemProvider(config.Config{
		BeemAPIKey: "api-key", BeemSecretKey: "secret-key", BeemSenderID: senderID,
	})
	p.Endpoint = b.srv.URL
	return p
}

// TestBeemSendSuccess pins the wire contract of API.md: basic auth, the exact
// body shape, and `request_id` read back as the provider message id.
func TestBeemSendSuccess(t *testing.T) {
	b := newBeemServer(t, http.StatusOK, `{"successful":true,"request_id":31456,"code":100,"message":"Message Submitted Successfully"}`)
	p := beemProvider(b, "TMS")

	id, err := p.Send(context.Background(), "+255715000101", "Water maintenance Sunday 9am", "JJNE")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if id != "31456" {
		t.Errorf("provider_msg_id = %q, want 31456", id)
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("api-key:secret-key"))
	if got := b.gotHdr.Get("Authorization"); got != wantAuth {
		t.Errorf("Authorization = %q, want %q", got, wantAuth)
	}
	if got := b.gotReq["source_addr"]; got != "JJNE" {
		t.Errorf("source_addr = %v, want the org's sender name JJNE", got)
	}
	if got, ok := b.gotReq["schedule_time"]; !ok || got != "" {
		t.Errorf("schedule_time = %v, want an empty string (send now)", got)
	}
	if got := b.gotReq["encoding"]; got != float64(0) {
		t.Errorf("encoding = %v, want 0", got)
	}
	if got := b.gotReq["message"]; got != "Water maintenance Sunday 9am" {
		t.Errorf("message = %v", got)
	}
	recipients, ok := b.gotReq["recipients"].([]any)
	if !ok || len(recipients) != 1 {
		t.Fatalf("recipients = %v, want exactly one", b.gotReq["recipients"])
	}
	rec, _ := recipients[0].(map[string]any)
	if rec["recipient_id"] != float64(1) {
		t.Errorf("recipient_id = %v, want 1", rec["recipient_id"])
	}
	// Beem wants the country code without the leading plus.
	if rec["dest_addr"] != "255715000101" {
		t.Errorf("dest_addr = %v, want 255715000101", rec["dest_addr"])
	}
}

// TestBeemSenderFallback walks the three rungs of SPEC §6: the org's own
// approved name, the platform's BEEM_SENDER_ID, and the built-in default.
func TestBeemSenderFallback(t *testing.T) {
	cases := []struct{ name, orgSender, platform, want string }{
		{"org sender wins", "JJNE", "TMS", "JJNE"},
		{"platform default", "", "TMS", "TMS"},
		{"built-in default", "", "", notify.DefaultSenderID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBeemServer(t, http.StatusOK, `{"successful":true,"request_id":"1"}`)
			p := beemProvider(b, tc.platform)
			if _, err := p.Send(context.Background(), "+255715000101", "hi", tc.orgSender); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if got := b.gotReq["source_addr"]; got != tc.want {
				t.Errorf("source_addr = %v, want %q", got, tc.want)
			}
		})
	}
}

// TestBeemSendFailures: every way a send can go wrong is an error the worker
// can record, never a silently dropped message.
func TestBeemSendFailures(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		response string
		wantIn   string
	}{
		{"non-2xx", http.StatusUnauthorized, `{"code":101,"message":"Invalid credentials"}`, "http 401"},
		{"successful false", http.StatusOK, `{"successful":false,"code":104,"message":"Invalid sender id"}`, "Invalid sender id"},
		{"unparseable body", http.StatusOK, `not json at all`, "decode response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBeemServer(t, tc.status, tc.response)
			p := beemProvider(b, "TMS")
			id, err := p.Send(context.Background(), "+255715000101", "hi", "")
			if err == nil {
				t.Fatalf("Send() succeeded with id %q, want an error", id)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantIn)
			}
			if id != "" {
				t.Errorf("provider_msg_id = %q on a failure, want empty", id)
			}
		})
	}
}

// TestSourceAddrIsSanitised: the sender name reaches the provider as a legal
// alphanumeric sender ID whatever is stored against the org.
func TestSourceAddrIsSanitised(t *testing.T) {
	p := &notify.BeemProvider{APIKey: "k", SecretKey: "s", SenderID: "PLATFORM"}
	cases := map[string]string{
		"JJNE":                 "JJNE",
		" JJnE Rentals ":       "JJnERentals",
		"JJnE Rentals Limited": "JJnERentals",
		"BAD\nX-Header: y":     "BADXHeadery",
		"!!!":                  "PLATFORM",
		"":                     "PLATFORM",
	}
	for in, want := range cases {
		if got := p.SourceAddr(in); got != want {
			t.Errorf("SourceAddr(%q) = %q, want %q", in, got, want)
		}
	}
	// With no platform default either, the provider's own fallback stands.
	bare := &notify.BeemProvider{APIKey: "k", SecretKey: "s"}
	if got := bare.SourceAddr("###"); got != notify.DefaultSenderID {
		t.Errorf("SourceAddr with no usable name = %q, want %q", got, notify.DefaultSenderID)
	}
}
