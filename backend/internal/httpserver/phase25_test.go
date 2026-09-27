package httpserver_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// Phase 25 — the landlord's assist screen (PLAN2 Phase 25, FLOWS 2b step 3).
//
// The screen shows the code *and* a QR of the renter's link. Both the opening
// call and every "New code" carry the link and its QR, because the session
// read never does: a landlord reopening a session gets a fresh code, and with
// it everything the first screen showed.

// TestAssistRevealsCarryTheLinkQR checks the QR is a PNG data URL on both
// reveal routes, and that the refresh repeats the same link.
func TestAssistRevealsCarryTheLinkQR(t *testing.T) {
	h := newHarness(t)
	f := newAssistFixture(t, h, "AsstQR", "0716250100", "+255716250101")

	opened := f.open(t).mustStatus(t, http.StatusCreated, "open assist session")
	link := opened.str(t, "link")
	mustPNGDataURL(t, opened.str(t, "link_qr"), "open")

	id := opened.str(t, "session", "id")
	refreshed := f.owner.do(http.MethodPost, "/assist/"+id+"/code", nil).
		mustStatus(t, http.StatusOK, "new code")
	if got := refreshed.str(t, "link"); got != link {
		t.Errorf("refresh link = %q, want the opening link %q", got, link)
	}
	mustPNGDataURL(t, refreshed.str(t, "link_qr"), "refresh")

	// The polled read stays small and code-free: no link, no QR.
	got := f.owner.do(http.MethodGet, "/assist/"+id, nil).mustStatus(t, http.StatusOK, "read session")
	if _, ok := got.Body["link_qr"]; ok {
		t.Error("GET /assist/{id} carries link_qr; only the reveal routes should")
	}
}

func mustPNGDataURL(t *testing.T, v, what string) {
	t.Helper()
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(v, prefix) {
		t.Fatalf("%s: link_qr = %.40q…, want a PNG data URL", what, v)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, prefix))
	if err != nil {
		t.Fatalf("%s: link_qr is not base64: %v", what, err)
	}
	if len(raw) < 8 || string(raw[1:4]) != "PNG" {
		t.Fatalf("%s: link_qr does not decode to a PNG", what)
	}
}
