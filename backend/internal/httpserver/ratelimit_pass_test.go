package httpserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"tms/backend/internal/httpserver"
)

// The SPEC §8 rate-limit pass.
//
// SPEC §8 says "rate limiting (Redis): auth endpoints, OTP, public QR
// resolution" and SPEC §3 fixes two of the numbers (3 OTP sends / 10 min per
// phone, 5 verify attempts). The rest were settled per endpoint as the phases
// landed. This table is where they are written down together, so a limit that
// is quietly dropped from a handler fails a test rather than going unnoticed
// until someone finds the bill.
//
// Every case drives the endpoint `limit` times, then once more, and requires
// that last call to answer 429 with a usable `Retry-After` header. The limiter
// counts against the in-process Redis the harness provides (miniredis), so the
// windows are real counters and not stubs.

// rlCase is one limited endpoint.
type rlCase struct {
	name string
	// limit is the documented number of calls allowed in the window.
	limit int
	// call issues attempt i (0-based) and returns the recorder, so the test can
	// read headers as well as the status.
	call func(t *testing.T, h *harness, i int) *rlResponse
	// cleanBelow requires every attempt up to the limit to be something other
	// than 429. It is false where a second, tighter guard can legitimately
	// answer 429 first: the OTP endpoints also enforce a 60-second resend
	// cooldown, which fires before the window counter fills.
	cleanBelow bool
	// why records where the number comes from.
	why string
}

// rlResponse is a response plus the headers the limiter sets.
type rlResponse struct {
	Code       int
	RetryAfter string
	Raw        string
}

// rlDo issues a request and captures the Retry-After header alongside the body.
// `client.do` drops headers, and Retry-After is half of what this file asserts.
func rlDo(c *client, method, path string, body any) *rlResponse {
	c.h.t.Helper()

	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.h.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, httpserver.APIPrefix+path, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	c.h.srv.Handler().ServeHTTP(rec, req)

	return &rlResponse{
		Code:       rec.Code,
		RetryAfter: rec.Header().Get("Retry-After"),
		Raw:        rec.Body.String(),
	}
}

// TestRateLimitPass drives every limited endpoint one call past its limit.
func TestRateLimitPass(t *testing.T) {
	cases := []rlCase{
		{
			name: "POST /auth/otp/send", limit: 3, why: "SPEC §3: 3 sends / 10 min per phone",
			call: func(_ *testing.T, h *harness, i int) *rlResponse {
				return rlDo(h.client(), http.MethodPost, "/auth/otp/send",
					map[string]any{"phone": "+255716000001", "purpose": "register"})
			},
			// The 60-second resend cooldown answers 429 from the second send
			// on; the window counter is what answers the fourth.
			cleanBelow: false,
		},
		{
			name: "POST /auth/otp/verify", limit: 5, why: "SPEC §3: 5 verify attempts",
			call: func(_ *testing.T, h *harness, i int) *rlResponse {
				return rlDo(h.client(), http.MethodPost, "/auth/otp/verify",
					map[string]any{"phone": "+255716000002", "code": "000000", "purpose": "register"})
			},
			cleanBelow: true,
		},
		{
			name: "POST /auth/login", limit: 10, why: "10 attempts / min per IP",
			call: func(_ *testing.T, h *harness, i int) *rlResponse {
				return rlDo(h.client(), http.MethodPost, "/auth/login",
					map[string]any{"email": "nobody@jjne.test", "password": "wrong-password"})
			},
			cleanBelow: true,
		},
		{
			name: "POST /orgs", limit: 5, why: "5 signups / hour per IP",
			call: func(_ *testing.T, h *harness, i int) *rlResponse {
				return rlDo(h.client(), http.MethodPost, "/orgs", map[string]any{
					"org_name": fmt.Sprintf("Rate Ltd %d", i), "owner_name": "Owner",
					"email":    fmt.Sprintf("rate%d@jjne.test", i),
					"phone":    fmt.Sprintf("07160100%02d", i),
					"password": "supersecret",
				})
			},
			cleanBelow: true,
		},
		{
			name: "GET /public/units/{unit_code}", limit: 60, why: "60 scans / min per IP",
			call: func(_ *testing.T, h *harness, i int) *rlResponse {
				return rlDo(h.client(), http.MethodGet, "/public/units/NOSUCHCODE", nil)
			},
			cleanBelow: true,
		},
		{
			// The public bucket is one counter per IP across both public
			// routes, so a scanner cannot double its budget by alternating.
			name: "GET /public/orgs/{slug}/branding", limit: 60, why: "shares the 60/min public IP bucket",
			call: func(_ *testing.T, h *harness, i int) *rlResponse {
				return rlDo(h.client(), http.MethodGet, "/public/orgs/no-such-org/branding", nil)
			},
			cleanBelow: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for i := 0; i < tc.limit; i++ {
				got := tc.call(t, h, i)
				if tc.cleanBelow && got.Code == http.StatusTooManyRequests {
					t.Fatalf("%s: attempt %d/%d was already limited (%s) — the window is tighter than %s",
						tc.name, i+1, tc.limit, got.Raw, tc.why)
				}
			}
			over := tc.call(t, h, tc.limit)
			assertLimited(t, tc.name+" ("+tc.why+")", over)
		})
	}
}

// TestRateLimitPassAuthenticated covers the limits that need a session: they
// are keyed by principal rather than by IP, so each needs its own fixture.
func TestRateLimitPassAuthenticated(t *testing.T) {
	t.Run("POST /contracts/{id}/sign/otp", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newContractFixture(t, "RateSign", "0716002000", "+255716002001")
		// 3 sends / 10 min per contract; the resend cooldown fires first, so
		// only the run past the window is asserted.
		var got *rlResponse
		for i := 0; i < 4; i++ {
			got = rlDo(fix.renter, http.MethodPost, "/contracts/"+fix.contractID+"/sign/otp", nil)
		}
		assertLimited(t, "sign OTP (3 / 10 min per contract)", got)
	})

	t.Run("POST /contracts/{id}/signature-upload", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newContractFixture(t, "RateSig", "0716002010", "+255716002011")
		body := map[string]any{"content_type": "image/png", "size_bytes": 2048}
		for i := 0; i < 5; i++ {
			got := rlDo(fix.renter, http.MethodPost,
				"/contracts/"+fix.contractID+"/signature-upload", body)
			if got.Code == http.StatusTooManyRequests {
				t.Fatalf("signature upload attempt %d was already limited: %s", i+1, got.Raw)
			}
		}
		assertLimited(t, "signature upload (5 / min per user)",
			rlDo(fix.renter, http.MethodPost, "/contracts/"+fix.contractID+"/signature-upload", body))
	})

	t.Run("POST /me/profile/kyc-upload", func(t *testing.T) {
		h := newHarness(t)
		renter := h.registerRenter("+255716002021", "Rate KYC", defaultPIN)
		body := map[string]any{"content_type": "image/jpeg", "size_bytes": 4096}
		for i := 0; i < 10; i++ {
			got := rlDo(renter, http.MethodPost, "/me/profile/kyc-upload", body)
			if got.Code == http.StatusTooManyRequests {
				t.Fatalf("kyc upload attempt %d was already limited: %s", i+1, got.Raw)
			}
		}
		assertLimited(t, "kyc upload presign (10 / min per user)",
			rlDo(renter, http.MethodPost, "/me/profile/kyc-upload", body))
	})

	t.Run("POST /units/{unit_code}/link", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newOrgWithUnits("RateLink", "ratelink@jjne.test", "0716002030",
			[]string{"L1", "L2", "L3", "L4", "L5", "L6"}, testUnitAmount)
		periodID := fix.client.periodIDByDays(t, 30)
		renter := h.registerRenter("+255716002031", "Rate Link", defaultPIN)
		renter.completeProfile(t, "Rate Link", validNIDA)

		for i := 0; i < 5; i++ {
			got := rlDo(renter, http.MethodPost, "/units/"+fix.unitCodes[i]+"/link",
				linkBody(periodID, testTermDays))
			if got.Code == http.StatusTooManyRequests {
				t.Fatalf("link request %d was already limited: %s", i+1, got.Raw)
			}
			if got.Code != http.StatusCreated {
				t.Fatalf("link request %d: status = %d, want 201 — %s", i+1, got.Code, got.Raw)
			}
		}
		assertLimited(t, "link request (5 / hour per renter)",
			rlDo(renter, http.MethodPost, "/units/"+fix.unitCodes[5]+"/link",
				linkBody(periodID, testTermDays)))
	})

	t.Run("POST /notifications/custom", func(t *testing.T) {
		h := newHarness(t)
		fix := h.newOrgWithUnits("RateSMS", "ratesms@jjne.test", "0716002040",
			[]string{"S1"}, testUnitAmount)
		body := map[string]any{"recipients": "all_active", "body": "Habari, maji yatakatika kesho."}
		for i := 0; i < 10; i++ {
			got := rlDo(fix.client, http.MethodPost, "/notifications/custom", body)
			if got.Code == http.StatusTooManyRequests {
				t.Fatalf("custom SMS batch %d was already limited: %s", i+1, got.Raw)
			}
		}
		assertLimited(t, "custom SMS batches (10 / hour per org)",
			rlDo(fix.client, http.MethodPost, "/notifications/custom", body))
	})
}

// assertLimited is the shared expectation: 429 with a Retry-After a client can
// actually wait on. A 429 without one tells the caller to guess, which in
// practice means retrying immediately.
func assertLimited(t *testing.T, what string, got *rlResponse) {
	t.Helper()
	if got.Code != http.StatusTooManyRequests {
		t.Fatalf("%s: status = %d, want 429 — the limit is not enforced. Body: %s",
			what, got.Code, got.Raw)
	}
	if got.RetryAfter == "" {
		t.Fatalf("%s: 429 carries no Retry-After header", what)
	}
	secs, err := strconv.Atoi(got.RetryAfter)
	if err != nil || secs <= 0 {
		t.Fatalf("%s: Retry-After = %q, want a positive whole number of seconds", what, got.RetryAfter)
	}
	if !strings.Contains(got.Raw, "too many requests") {
		t.Errorf("%s: 429 body is not the RFC-7807 problem document: %s", what, got.Raw)
	}
}
