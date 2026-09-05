package httpserver_test

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tms/backend/internal/config"
	"tms/backend/internal/httpserver"
)

// The SPEC §8 isolation suite.
//
// The per-phase tests each check the isolation of the routes that phase added.
// This file checks it for *every* registered route, and — the part that keeps
// working after this phase — fails when a route exists that the table does not
// mention. A new endpoint therefore cannot ship without its author deciding, in
// writing, what another org is allowed to see through it.
//
// Three runs share one table:
//
//   - org      — org B's owner calls a path naming org A's rows. 404, always:
//     "belongs to someone else" must be indistinguishable from
//     "does not exist" (API.md). Collection routes have no A id to
//     name, so they are called plainly and the response is searched
//     for A's ids instead.
//   - renter   — renter 2 calls a path naming renter 1's rows. Same rule.
//   - admin    — an org owner and a renter call the platform routes. 401: the
//     admin audience is a different cookie, not a stronger role.
//
// Every response in the org and renter runs is additionally swept for any of
// org A's identifiers, so a route that answers 200 while leaking a foreign id
// in its body fails even when its status code looks right.

// ------------------------------------------------------------ the table --

type isoAudience string

const (
	// isoOrg  — exercised with org B's owner session.
	isoOrg isoAudience = "org"
	// isoRenter — exercised with renter 2's session.
	isoRenter isoAudience = "renter"
	// isoParty — a contract read open to either party: run twice, once as org
	// B and once as renter 2.
	isoParty isoAudience = "party"
	// isoAdmin — platform route: run as an org owner and as a renter.
	isoAdmin isoAudience = "admin"
	// isoOpen — deliberately not org-scoped. `note` says why, and is required.
	isoOpen isoAudience = "open"
)

// isoCase is one registered route and what another tenant may do with it.
type isoCase struct {
	method  string      // HTTP method, as chi reports it
	pattern string      // chi pattern below APIPrefix, e.g. "/units/{id}"
	aud     isoAudience // who exercises it
	// path is the concrete request path, with {placeholders} filled from the
	// fixture (see isoFixture.replacer). Empty means "same as pattern" — only
	// valid for patterns carrying no {param}.
	path string
	// body is the minimal request body that gets the handler past its own
	// validation and as far as the scope check. String values are placeholder-
	// substituted too, so a body can name org A's rows.
	body map[string]any
	// want is the set of acceptable statuses. Empty means {404}: the default
	// for a path naming another tenant's entity.
	want []int
	// note explains an isoOpen entry. Required for those, ignored otherwise.
	note string
}

// isoRoutes is the coverage table. TestIsolationSuiteCoversEveryRoute asserts
// it matches the router exactly in both directions, so this list is the
// authoritative census of the API surface.
//
//nolint:gochecknoglobals // a fixed table, read-only.
var isoRoutes = []isoCase{
	// ------------------------------------------------------------- open --
	{method: "GET", pattern: "/healthz", aud: isoOpen, note: "liveness probe; no tenant data"},
	{method: "POST", pattern: "/auth/otp/send", aud: isoOpen, note: "pre-auth; keyed by phone, not by org"},
	{method: "POST", pattern: "/auth/otp/verify", aud: isoOpen, note: "pre-auth; keyed by phone, not by org"},
	{method: "POST", pattern: "/auth/register/renter", aud: isoOpen, note: "pre-auth; creates a platform-level renter"},
	{method: "POST", pattern: "/auth/login", aud: isoOpen, note: "pre-auth; the session is the org scope, not an input"},
	{method: "POST", pattern: "/auth/logout", aud: isoOpen, note: "clears the caller's own cookie"},
	{method: "GET", pattern: "/auth/me", aud: isoOpen, note: "returns the caller's own session"},
	{method: "POST", pattern: "/auth/verify-email", aud: isoOpen, note: "single-use token is the authorisation"},
	{method: "POST", pattern: "/auth/invite/accept", aud: isoOpen, note: "single-use token is the authorisation"},
	{method: "POST", pattern: "/orgs", aud: isoOpen, note: "creates a new tenant; no tenant to be scoped to yet"},
	{
		method: "GET", pattern: "/public/orgs/{slug}/branding", aud: isoOpen,
		note: "deliberately public: a login page is branded before anyone has signed in (SPEC §2.1)",
	},
	{
		method: "GET", pattern: "/public/units/{unit_code}", aud: isoOpen,
		note: "deliberately public: the QR sticker resolves before the scanner has an account (SPEC §3.1)",
	},

	// ------------------------------------------ org: the caller's own org --
	{method: "GET", pattern: "/org", aud: isoOrg, want: []int{200}},
	{method: "PATCH", pattern: "/org", aud: isoOrg, body: map[string]any{"name": "Org Beta"}, want: []int{200}},
	{
		method: "POST", pattern: "/auth/verify-email/resend", aud: isoOrg,
		want: []int{200, 202, 204, 409},
	},
	{method: "GET", pattern: "/org/members", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/org/members", aud: isoOrg,
		body: map[string]any{"email": "iso-staff@beta.test", "full_name": "Beta Staff", "role": "org_manager"},
		want: []int{201},
	},
	{method: "DELETE", pattern: "/org/members/{id}", aud: isoOrg, path: "/org/members/{memberA}"},
	{method: "GET", pattern: "/audit-log", aud: isoOrg, want: []int{200}},
	{method: "GET", pattern: "/audit-log/{id}", aud: isoOrg, path: "/audit-log/{auditA}"},

	// --------------------------------------------------- payment periods --
	{method: "GET", pattern: "/org/payment-periods", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/org/payment-periods", aud: isoOrg,
		body: map[string]any{"label": "Iso 14 days", "days": 14}, want: []int{201},
	},
	{
		method: "POST", pattern: "/org/payment-periods/restore-recommended", aud: isoOrg,
		want: []int{200},
	},
	{
		method: "PATCH", pattern: "/org/payment-periods/{id}", aud: isoOrg,
		path: "/org/payment-periods/{periodA}", body: map[string]any{"label": "Hijacked"},
	},
	{
		method: "DELETE", pattern: "/org/payment-periods/{id}", aud: isoOrg,
		path: "/org/payment-periods/{periodA}",
	},

	// -------------------------------------------------------- properties --
	{method: "GET", pattern: "/properties", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/properties", aud: isoOrg,
		body: map[string]any{"name": "Beta Block", "location_text": "Dar es Salaam"}, want: []int{201},
	},
	{method: "GET", pattern: "/properties/{id}", aud: isoOrg, path: "/properties/{propertyA}"},
	{
		method: "PATCH", pattern: "/properties/{id}", aud: isoOrg,
		path: "/properties/{propertyA}", body: map[string]any{"name": "Hijacked"},
	},
	{method: "DELETE", pattern: "/properties/{id}", aud: isoOrg, path: "/properties/{propertyA}"},
	{method: "GET", pattern: "/properties/{id}/units", aud: isoOrg, path: "/properties/{propertyA}/units"},
	{
		method: "POST", pattern: "/properties/{id}/units", aud: isoOrg,
		path: "/properties/{propertyA}/units", body: map[string]any{"name": "Smuggled"},
	},
	{
		method: "POST", pattern: "/properties/{id}/units/bulk", aud: isoOrg,
		path: "/properties/{propertyA}/units/bulk", body: map[string]any{"names": []any{"Smuggled"}},
	},
	{method: "GET", pattern: "/properties/{id}/qr-sheet", aud: isoOrg, path: "/properties/{propertyA}/qr-sheet"},

	// ------------------------------------------------------ units, prices --
	{method: "GET", pattern: "/units", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/units/bulk-price", aud: isoOrg,
		body: map[string]any{"unit_ids": []any{"{unitA}"}, "mode": "set", "value": 100000, "period_days": 30},
		want: []int{404},
	},
	{method: "GET", pattern: "/units/{id}", aud: isoOrg, path: "/units/{unitA}"},
	{
		method: "PATCH", pattern: "/units/{id}", aud: isoOrg,
		path: "/units/{unitA}", body: map[string]any{"name": "Hijacked"},
	},
	{method: "DELETE", pattern: "/units/{id}", aud: isoOrg, path: "/units/{unitA}"},
	{method: "POST", pattern: "/units/{id}/qr", aud: isoOrg, path: "/units/{unitA}/qr"},
	{method: "GET", pattern: "/units/{id}/prices", aud: isoOrg, path: "/units/{unitA}/prices"},
	{
		method: "POST", pattern: "/units/{id}/prices", aud: isoOrg,
		path: "/units/{unitA}/prices", body: map[string]any{"amount": 500000, "period_days": 30},
	},

	// -------------------------------------- link requests, renter directory --
	{method: "GET", pattern: "/link-requests", aud: isoOrg, want: []int{200}},
	{method: "GET", pattern: "/link-requests/{id}", aud: isoOrg, path: "/link-requests/{linkRequestA}"},
	{
		method: "POST", pattern: "/link-requests/{id}/approve", aud: isoOrg,
		path: "/link-requests/{linkRequestA}/approve",
	},
	{
		method: "POST", pattern: "/link-requests/{id}/reject", aud: isoOrg,
		path: "/link-requests/{linkRequestA}/reject", body: map[string]any{"reason": "not for you"},
	},
	{method: "GET", pattern: "/renters", aud: isoOrg, want: []int{200}},
	{method: "GET", pattern: "/renters/{user_id}", aud: isoOrg, path: "/renters/{renterUserA}"},
	{method: "GET", pattern: "/renters/{user_id}/kyc-doc", aud: isoOrg, path: "/renters/{renterUserA}/kyc-doc"},

	// -------------------------------------------------- contract templates --
	{method: "GET", pattern: "/contract-templates", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/contract-templates", aud: isoOrg,
		body: map[string]any{"name": "Beta Terms", "body_html": "<p>Beta</p>"}, want: []int{201},
	},
	{method: "GET", pattern: "/contract-templates/{id}", aud: isoOrg, path: "/contract-templates/{templateA}"},
	{
		method: "PATCH", pattern: "/contract-templates/{id}", aud: isoOrg,
		path: "/contract-templates/{templateA}", body: map[string]any{"name": "Hijacked"},
	},
	{method: "DELETE", pattern: "/contract-templates/{id}", aud: isoOrg, path: "/contract-templates/{templateA}"},
	{
		method: "POST", pattern: "/contract-templates/{id}/preview", aud: isoOrg,
		path: "/contract-templates/{templateA}/preview", body: map[string]any{"sample": true},
	},

	// ----------------------------------------------------------- contracts --
	{method: "GET", pattern: "/contracts", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/contracts", aud: isoOrg,
		body: map[string]any{
			"unit_id": "{unitVacantA}", "renter_user_id": "{renterUserA}",
			"payment_period_id": "{periodA}", "term_days": 180, "start_date": "{today}",
		},
	},
	{method: "POST", pattern: "/contracts/{id}/activate", aud: isoOrg, path: "/contracts/{contractA}/activate"},
	{
		method: "POST", pattern: "/contracts/{id}/terminate", aud: isoOrg,
		path: "/contracts/{contractA}/terminate",
		body: map[string]any{"reason": "hijack", "effective_date": "{today}"},
	},

	// -------------------------------- contract reads, open to either party --
	{method: "GET", pattern: "/contracts/{id}", aud: isoParty, path: "/contracts/{contractA}"},
	{method: "GET", pattern: "/contracts/{id}/document", aud: isoParty, path: "/contracts/{contractA}/document"},
	{method: "GET", pattern: "/contracts/{id}/verify", aud: isoParty, path: "/contracts/{contractA}/verify"},
	{method: "GET", pattern: "/contracts/{id}/schedules", aud: isoParty, path: "/contracts/{contractA}/schedules"},

	// --------------------------------------- schedules, payments, banking --
	{method: "GET", pattern: "/schedules", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/payments", aud: isoOrg,
		body: map[string]any{
			"contract_id": "{contractA}", "schedule_id": "{scheduleA}",
			"amount": 1000, "method": "cash",
		},
	},
	{method: "GET", pattern: "/payments", aud: isoOrg, want: []int{200}},
	{method: "GET", pattern: "/payments/{id}", aud: isoOrg, path: "/payments/{paymentA}"},
	{
		method: "POST", pattern: "/payments/{id}/reverse", aud: isoOrg,
		path: "/payments/{paymentA}/reverse", body: map[string]any{"reason": "hijack"},
	},
	{method: "GET", pattern: "/org/bank-account", aud: isoOrg, want: []int{200}},
	{
		method: "PUT", pattern: "/org/bank-account", aud: isoOrg,
		body: map[string]any{
			"bank_name": "CRDB", "account_name": "Org Beta", "account_number": "0150000000000",
		},
		want: []int{200},
	},

	// ------------------------------------------------------ notifications --
	{method: "GET", pattern: "/org/notification-settings", aud: isoOrg, want: []int{200}},
	{
		method: "PUT", pattern: "/org/notification-settings", aud: isoOrg,
		body: map[string]any{"sender_name": "BETA", "send_hour_local": 9, "language": "sw"},
		want: []int{200},
	},
	{
		// DECISIONS.md: naming another org's renter counts as `skipped`, never
		// a 404 — a broadcast must not become a directory probe. The leak
		// sweep is what proves nothing of A's came back.
		method: "POST", pattern: "/notifications/custom", aud: isoOrg,
		body: map[string]any{
			"recipients": "selected", "renter_user_ids": []any{"{renterUserA}"},
			"body": "Habari, hii ni ujumbe wa majaribio.",
		},
		want: []int{200, 202},
	},
	{method: "GET", pattern: "/notifications/log", aud: isoOrg, want: []int{200}},
	{
		method: "POST", pattern: "/notifications/log/{id}/retry", aud: isoOrg,
		path: "/notifications/log/{notificationA}/retry",
	},

	// ------------------------------------------------------------ reports --
	{method: "GET", pattern: "/reports/summary", aud: isoOrg, want: []int{200}},
	{method: "GET", pattern: "/reports/payment-status", aud: isoOrg, want: []int{200}},
	{method: "GET", pattern: "/reports/collections", aud: isoOrg, want: []int{200}},

	// ----------------------------------------------------------- branding --
	{method: "GET", pattern: "/org/branding", aud: isoOrg, want: []int{200}},
	{
		method: "PUT", pattern: "/org/branding", aud: isoOrg,
		body: map[string]any{"display_name": "Beta Rentals"}, want: []int{200},
	},
	{
		method: "POST", pattern: "/org/branding/logo", aud: isoOrg,
		body: map[string]any{"content_type": "image/png", "size_bytes": 1024},
		want: []int{200, 201, 503},
	},
	{
		// Claiming an object under org A's prefix must be refused on the key
		// alone, before MinIO is ever asked whether it exists.
		method: "POST", pattern: "/org/branding/logo/complete", aud: isoOrg,
		body: map[string]any{"object_key": "{orgA}/logo.png"},
		want: []int{400, 503},
	},
	{method: "DELETE", pattern: "/org/branding/logo", aud: isoOrg, want: []int{200, 204}},
	{
		method: "POST", pattern: "/org/branding/letterhead", aud: isoOrg,
		body: map[string]any{"content_type": "image/png", "size_bytes": 1024},
		want: []int{200, 201, 503},
	},
	{
		method: "POST", pattern: "/org/branding/letterhead/complete", aud: isoOrg,
		body: map[string]any{"object_key": "{orgA}/letterhead.png"},
		want: []int{400, 503},
	},
	{method: "DELETE", pattern: "/org/branding/letterhead", aud: isoOrg, want: []int{200, 204}},

	// ------------------------------------------------------ renter scope --
	{method: "GET", pattern: "/me/profile", aud: isoRenter, want: []int{200}},
	{
		method: "PUT", pattern: "/me/profile", aud: isoRenter,
		body: map[string]any{
			"full_name": "Renter Two", "nida_number": validNIDA,
			"next_of_kin_name": "Kin Two", "next_of_kin_phone": "+255713999999",
		},
		want: []int{200},
	},
	{
		method: "POST", pattern: "/me/profile/kyc-upload", aud: isoRenter,
		body: map[string]any{"content_type": "image/jpeg", "size_bytes": 2048},
		want: []int{200, 201, 503},
	},
	{
		// The key names renter 1's folder; the shape check refuses it without
		// ever reaching object storage.
		method: "POST", pattern: "/me/profile/kyc-upload/complete", aud: isoRenter,
		body: map[string]any{"object_key": "{renterUserA}/id.jpg"},
		want: []int{400, 503},
	},
	{method: "GET", pattern: "/me/profile/kyc-doc", aud: isoRenter, want: []int{200, 404}},
	{method: "GET", pattern: "/me/link-requests", aud: isoRenter, want: []int{200}},
	{
		method: "DELETE", pattern: "/me/link-requests/{id}", aud: isoRenter,
		path: "/me/link-requests/{linkRequestA}",
	},
	{
		// A renter may legitimately apply to any org's unit — that is the QR
		// flow. What must not happen is seeing anything of renter 1's.
		method: "POST", pattern: "/units/{unit_code}/link", aud: isoRenter,
		path: "/units/{unitVacantCodeA}/link",
		body: map[string]any{
			"payment_period_id": "{periodA}", "term_days": 180,
			"start_date": "{today}", "accepted_terms": true,
		},
		want: []int{201},
	},
	{method: "GET", pattern: "/me/contracts", aud: isoRenter, want: []int{200}},
	{method: "GET", pattern: "/me/schedules", aud: isoRenter, want: []int{200}},
	{method: "GET", pattern: "/me/payments", aud: isoRenter, want: []int{200}},
	{
		method: "POST", pattern: "/contracts/{id}/sign/otp", aud: isoRenter,
		path: "/contracts/{contractA}/sign/otp",
	},
	{
		method: "POST", pattern: "/contracts/{id}/signature-upload", aud: isoRenter,
		path: "/contracts/{contractA}/signature-upload",
		body: map[string]any{"content_type": "image/png", "size_bytes": 2048},
	},
	{
		method: "POST", pattern: "/contracts/{id}/sign", aud: isoRenter,
		path: "/contracts/{contractA}/sign", body: map[string]any{"otp_code": "123456"},
	},

	// ------------------------------------------------------ platform admin --
	{method: "POST", pattern: "/admin/jobs/contract-lifecycle", aud: isoAdmin},
	{method: "POST", pattern: "/admin/jobs/overdue", aud: isoAdmin},
	{method: "POST", pattern: "/admin/jobs/notifications", aud: isoAdmin},
	{method: "GET", pattern: "/admin/jobs", aud: isoAdmin},
	{method: "GET", pattern: "/admin/orgs", aud: isoAdmin},
	{method: "GET", pattern: "/admin/orgs/{id}", aud: isoAdmin, path: "/admin/orgs/{orgA}"},
	{
		method: "POST", pattern: "/admin/orgs/{id}/suspend", aud: isoAdmin,
		path: "/admin/orgs/{orgA}/suspend", body: map[string]any{"reason": "hijack"},
	},
	{method: "POST", pattern: "/admin/orgs/{id}/activate", aud: isoAdmin, path: "/admin/orgs/{orgA}/activate"},
	{method: "GET", pattern: "/admin/metrics", aud: isoAdmin},
	{method: "GET", pattern: "/admin/audit-log", aud: isoAdmin},
}

// ------------------------------------------------- the router census --

// routeKey is how a route is named in both the router walk and the table.
func routeKey(method, pattern string) string { return method + " " + pattern }

// walkRoutes returns every route the router has registered, below APIPrefix.
func walkRoutes(t *testing.T) map[string]bool {
	t.Helper()
	srv := httpserver.New(config.Config{Env: config.EnvDev, Port: "0"}, httpserver.Deps{}, discardLogger())
	routes, ok := srv.Handler().(chi.Routes)
	if !ok {
		t.Fatal("the router does not implement chi.Routes; the walk cannot enumerate it")
	}
	out := map[string]bool{}
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		pattern := strings.TrimSuffix(strings.TrimPrefix(route, httpserver.APIPrefix), "/")
		if pattern == "" {
			pattern = "/"
		}
		out[routeKey(method, pattern)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the router walk found no routes")
	}
	return out
}

// TestIsolationSuiteCoversEveryRoute is the ratchet. It needs no database: it
// compares the registered routes against the table in both directions, so a new
// endpoint fails the build until someone writes down what another tenant may do
// with it, and a deleted endpoint fails until its stale entry is removed.
func TestIsolationSuiteCoversEveryRoute(t *testing.T) {
	registered := walkRoutes(t)

	covered := map[string]isoCase{}
	for _, c := range isoRoutes {
		key := routeKey(c.method, c.pattern)
		if prev, dup := covered[key]; dup {
			t.Errorf("duplicate table entry for %s (audiences %q and %q)", key, prev.aud, c.aud)
			continue
		}
		covered[key] = c
	}

	var uncovered, stale []string
	for key := range registered {
		if _, ok := covered[key]; !ok {
			uncovered = append(uncovered, key)
		}
	}
	for key := range covered {
		if !registered[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(uncovered)
	sort.Strings(stale)

	if len(uncovered) > 0 {
		t.Errorf("%d registered route(s) have no isolation coverage — add an entry to isoRoutes "+
			"in isolation_suite_test.go saying what another tenant may do with them:\n  %s",
			len(uncovered), strings.Join(uncovered, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("%d isolation table entr(ies) name a route that is no longer registered:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}

	// Every isoOpen entry has to justify itself: "not org-scoped" is a claim,
	// and an unexplained one is how a leak gets waved through.
	for _, c := range isoRoutes {
		if c.aud == isoOpen && strings.TrimSpace(c.note) == "" {
			t.Errorf("%s is marked isoOpen with no note explaining why it is not org-scoped",
				routeKey(c.method, c.pattern))
		}
	}

	// The census, for the phase report.
	counts := map[isoAudience]int{}
	for _, c := range isoRoutes {
		counts[c.aud]++
	}
	t.Logf("isolation coverage: %d/%d registered routes — org %d, renter %d, either-party %d, admin %d, open %d",
		len(covered), len(registered),
		counts[isoOrg], counts[isoRenter], counts[isoParty], counts[isoAdmin], counts[isoOpen])
}

// ------------------------------------------------------------- fixture --

// isoFixture is org A, fully populated, beside an unrelated org B and an
// unrelated renter 2 — the two callers the suite runs as.
type isoFixture struct {
	h *harness

	ownerA  *client // org A: owns everything the ids below name
	ownerB  *client // org B: the caller in the org run
	renter1 *client // renter 1: org A's tenant
	renter2 *client // renter 2: the caller in the renter run

	ids map[string]string
}

// isoValue is the placeholder substitution: "{unitA}" becomes org A's unit id.
func (f *isoFixture) isoValue(s string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	for k, v := range f.ids {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// substitute walks a request body replacing placeholders in string values.
func (f *isoFixture) substitute(v any) any {
	switch t := v.(type) {
	case string:
		return f.isoValue(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = f.substitute(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = f.substitute(item)
		}
		return out
	default:
		return v
	}
}

// isoSecretIDs name the fixture ids that belong to org A and renter 1 alone. No
// response to org B or renter 2 may contain any of them.
//
// The ids deliberately left out are the ones the product publishes on purpose:
// a unit's code and id and the periods offered on it are what the QR sticker
// resolves to pre-auth (SPEC §3.1), so seeing them is the feature, not a leak.
//
//nolint:gochecknoglobals // fixed list, read-only.
var isoSecretIDs = []string{
	"orgA", "propertyA", "unitA", "contractA", "scheduleA", "paymentA",
	"templateA", "linkRequestA", "memberA", "auditA", "notificationA", "renterUserA",
}

func (f *isoFixture) secrets() map[string]string {
	out := make(map[string]string, len(isoSecretIDs))
	for _, k := range isoSecretIDs {
		if v := f.ids[k]; v != "" {
			out[k] = v
		}
	}
	return out
}

// assertNoLeak fails when a response carries any of org A's identifiers that
// the caller did not already put in the request. An id echoed back out of the
// request (a 404 quoting the path, say) tells the caller nothing they did not
// type; an id that arrives unprompted is a leak.
func (f *isoFixture) assertNoLeak(t *testing.T, what, sent, raw string) {
	t.Helper()
	for name, id := range f.secrets() {
		if strings.Contains(sent, id) {
			continue
		}
		if strings.Contains(raw, id) {
			t.Errorf("%s leaked org A's %s (%s) into the response body: %s", what, name, id, raw)
		}
	}
}

// newIsoFixture builds org A with one of every org-scoped row, then the two
// unrelated principals that will try to reach them.
func newIsoFixture(t *testing.T, h *harness) *isoFixture {
	t.Helper()

	// Org A, a renter, an active contract, a recorded payment: the fixture from
	// Phase 5, which has already walked the real flow end to end.
	base := h.newPaymentFixture(t, "IsoAlpha", "0715080100", "+255715080101")
	f := &isoFixture{h: h, ownerA: base.owner, renter1: base.renter, ids: map[string]string{}}

	f.ids["today"] = time.Now().UTC().Format("2006-01-02")
	f.ids["orgA"] = base.orgID
	f.ids["contractA"] = base.contractID
	f.ids["scheduleA"] = base.scheduleIDs[0]
	f.ids["renterUserA"] = base.renterID
	f.ids["periodA"] = base.periodID
	f.ids["unitA"] = base.unitIDs[0]
	f.ids["unitVacantA"] = base.unitIDs[1]
	f.ids["unitVacantCodeA"] = base.unitCodes[1]

	f.ids["propertyA"] = firstID(t, base.owner.do(http.MethodGet, "/properties", nil).
		mustStatus(t, http.StatusOK, "org A properties"))

	// A payment to reverse.
	f.ids["paymentA"] = base.owner.recordPayment(map[string]any{
		"contract_id": base.contractID, "schedule_id": base.scheduleIDs[0],
		"amount": base.amounts[0], "method": "cash", "reference": "ISO-A",
	}).mustStatus(t, http.StatusCreated, "org A payment").str(t, "payment", "id")

	// A template to read, patch and delete.
	f.ids["templateA"] = base.owner.do(http.MethodPost, "/contract-templates", map[string]any{
		"name": "Alpha Terms", "body_html": "<p>Alpha terms for {{renter_name}}</p>",
	}).mustStatus(t, http.StatusCreated, "org A template").str(t, "template", "id")

	// A pending application to approve or reject.
	f.ids["linkRequestA"] = base.renter.do(http.MethodPost, "/units/"+base.unitCodes[2]+"/link",
		linkBody(base.periodID, testTermDays)).
		mustStatus(t, http.StatusCreated, "org A pending application").str(t, "request", "id")

	// A staff member to remove.
	f.ids["memberA"] = base.owner.do(http.MethodPost, "/org/members", map[string]any{
		"email": "alpha-staff@iso.test", "full_name": "Alpha Staff", "role": "org_manager",
	}).mustStatus(t, http.StatusCreated, "org A member").str(t, "member", "id")

	// An audit row and a notification row: both exist because the flow above
	// wrote them, so the ids are read back rather than manufactured.
	f.ids["auditA"] = firstID(t, base.owner.do(http.MethodGet, "/audit-log", nil).
		mustStatus(t, http.StatusOK, "org A audit log"))
	f.ids["notificationA"] = firstID(t, base.owner.do(http.MethodGet, "/notifications/log", nil).
		mustStatus(t, http.StatusOK, "org A notification log"))

	// The two callers, related to none of the above.
	f.ownerB, _ = h.createOrg("Iso Beta", "Bob Beta", "iso-beta@jjne.test", "0715080200", "supersecret")
	f.renter2 = h.registerRenter("+255715080201", "Iso Renter Two", defaultPIN)
	f.renter2.completeProfile(t, "Iso Renter Two", "19900101123456789013")

	return f
}

// firstID reads the id of the first row of a list response.
func firstID(t *testing.T, r response) string {
	t.Helper()
	rows := listOf(t, r)
	if len(rows) == 0 {
		t.Fatalf("expected at least one row to name in the isolation fixture — body: %s", r.Raw)
	}
	id, _ := rows[0]["id"].(string)
	if id == "" {
		t.Fatalf("first row carries no id — body: %s", r.Raw)
	}
	return id
}

// ------------------------------------------------------------- the runs --

// run issues one table case as the given client and checks status and leakage.
func (f *isoFixture) run(t *testing.T, c *client, caller string, tc isoCase) {
	t.Helper()

	path := tc.path
	if path == "" {
		path = tc.pattern
	}
	path = f.isoValue(path)
	if strings.ContainsAny(path, "{}") {
		t.Fatalf("%s: unresolved placeholder in path %q", routeKey(tc.method, tc.pattern), path)
	}

	var body any
	sent := path
	if tc.body != nil {
		body = f.substitute(tc.body)
		sent += " " + fmt.Sprint(body)
	}

	want := tc.want
	if len(want) == 0 {
		want = []int{http.StatusNotFound}
	}

	resp := c.do(tc.method, path, body)
	if !containsInt(want, resp.Code) {
		t.Errorf("%s as %s: status = %d, want one of %v — body: %s",
			routeKey(tc.method, tc.pattern), caller, resp.Code, want, resp.Raw)
	}
	f.assertNoLeak(t, fmt.Sprintf("%s as %s", routeKey(tc.method, tc.pattern), caller), sent, resp.Raw)
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// TestCrossOrgIsolationSuite runs every org-scoped route as another org's owner.
func TestCrossOrgIsolationSuite(t *testing.T) {
	h := newHarness(t)
	f := newIsoFixture(t, h)

	ran := 0
	for _, tc := range isoRoutes {
		if tc.aud != isoOrg && tc.aud != isoParty {
			continue
		}
		ran++
		t.Run(routeKey(tc.method, tc.pattern), func(t *testing.T) {
			f.run(t, f.ownerB, "org B owner", tc)
		})
	}
	t.Logf("cross-org isolation: %d org-scoped routes exercised as org B", ran)
}

// TestCrossRenterIsolationSuite runs every renter route as an unrelated renter.
func TestCrossRenterIsolationSuite(t *testing.T) {
	h := newHarness(t)
	f := newIsoFixture(t, h)

	ran := 0
	for _, tc := range isoRoutes {
		if tc.aud != isoRenter && tc.aud != isoParty {
			continue
		}
		ran++
		t.Run(routeKey(tc.method, tc.pattern), func(t *testing.T) {
			f.run(t, f.renter2, "renter 2", tc)
		})
	}
	t.Logf("cross-renter isolation: %d renter routes exercised as renter 2", ran)
}

// TestAdminRoutesRefuseTenantSessions checks the platform surface: an org owner
// and a renter hold valid sessions, but not for the admin audience.
func TestAdminRoutesRefuseTenantSessions(t *testing.T) {
	h := newHarness(t)
	f := newIsoFixture(t, h)

	callers := []struct {
		name string
		c    *client
	}{
		{"org A owner", f.ownerA},
		{"renter 1", f.renter1},
		{"anonymous", h.client()},
	}

	ran := 0
	for _, tc := range isoRoutes {
		if tc.aud != isoAdmin {
			continue
		}
		ran++
		for _, caller := range callers {
			t.Run(routeKey(tc.method, tc.pattern)+" as "+caller.name, func(t *testing.T) {
				probe := tc
				probe.want = []int{http.StatusUnauthorized}
				f.run(t, caller.c, caller.name, probe)
			})
		}
	}
	t.Logf("admin isolation: %d platform routes refused to %d tenant callers", ran, len(callers))
}
