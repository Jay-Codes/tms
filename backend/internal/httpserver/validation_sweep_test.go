package httpserver_test

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// The SPEC §8 input-validation sweep.
//
// "Input validation server-side on every endpoint" is easy to assert and hard
// to keep true: a handler added in a hurry that reads its body with a bare
// json.Unmarshal, or indexes a slice the body controls, answers 500 to a
// malformed request — which is both an availability bug and a way to learn
// what the server is doing internally.
//
// This sweep walks the router for every mutating route, and sends each of them
// three deliberately bad requests as a legitimate, authenticated caller:
//
//	empty         — a zero-byte body where an object is expected
//	unknown field — a well-formed object with a member no handler declares
//	oversized     — a well-formed object two MiB long (the cap is one)
//
// The rule is absolute: no 5xx, ever. Beyond that, any route that decodes JSON
// strictly (proved by its own 400 on the unknown-field probe) must also refuse
// the oversized body rather than read it.
//
// The route list comes from the router itself, and the concrete paths and
// audiences come from isoRoutes, so a new mutating endpoint is swept the moment
// it is added to the isolation table — which TestIsolationSuiteCoversEveryRoute
// already forces.

// sweepProbe is one malformed request shape.
type sweepProbe struct {
	name string
	// body is what to send. A nil body sends zero bytes.
	body func() any
}

// oversizedBody is one member whose value alone exceeds httpx.MaxBodyBytes.
func oversizedBody() any {
	return map[string]any{"note": strings.Repeat("a", 2<<20)}
}

//nolint:gochecknoglobals // fixed probe list, read-only.
var sweepProbes = []sweepProbe{
	{name: "empty body", body: func() any { return nil }},
	{name: "unknown field", body: func() any { return map[string]any{"totally_unknown_field": "x"} }},
	{name: "2 MiB body", body: oversizedBody},
}

// TestValidationSweep is the sweep proper.
func TestValidationSweep(t *testing.T) {
	h := newHarness(t)
	f := newIsoFixture(t, h)
	admin := h.adminClient(t)

	// The mutating half of the router, taken from the router itself.
	registered := walkRoutes(t)
	byKey := map[string]isoCase{}
	for _, c := range isoRoutes {
		byKey[routeKey(c.method, c.pattern)] = c
	}

	var keys []string
	for key := range registered {
		if !strings.HasPrefix(key, http.MethodPost+" ") &&
			!strings.HasPrefix(key, http.MethodPut+" ") &&
			!strings.HasPrefix(key, http.MethodPatch+" ") {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		t.Fatal("the router walk found no mutating routes")
	}

	var bodyless []string
	for _, key := range keys {
		tc, ok := byKey[key]
		if !ok {
			// TestIsolationSuiteCoversEveryRoute already fails on this; the
			// sweep says so too rather than silently skipping a route.
			t.Errorf("%s has no isoRoutes entry, so the validation sweep cannot reach it", key)
			continue
		}

		caller, name := sweepCaller(f, admin, h, tc)
		path := f.isoValue(orPattern(tc))
		if strings.ContainsAny(path, "{}") {
			t.Errorf("%s: unresolved placeholder in sweep path %q", key, path)
			continue
		}

		strict := false
		t.Run(key, func(t *testing.T) {
			for _, probe := range sweepProbes {
				resp := caller.do(tc.method, path, probe.body())

				// 503 is the honest answer when a dependency the route needs
				// (object storage) is not running, which is the normal state on
				// a machine without the compose stack. Every other 5xx is the
				// bug this sweep is looking for.
				if resp.Code >= http.StatusInternalServerError && resp.Code != http.StatusServiceUnavailable {
					t.Errorf("%s as %s: %s produced %d — a malformed request must never reach a 5xx. Body: %s",
						key, name, probe.name, resp.Code, resp.Raw)
					continue
				}
				switch probe.name {
				case "unknown field":
					if resp.Code == http.StatusBadRequest {
						strict = true
					}
				case "2 MiB body":
					if strict && resp.Code != http.StatusRequestEntityTooLarge &&
						resp.Code != http.StatusBadRequest {
						t.Errorf("%s as %s: a 2 MiB body answered %d; a route that decodes JSON "+
							"must cap the body it reads (413) rather than accept it. Body: %s",
							key, name, resp.Code, resp.Raw)
					}
				}
			}
		})
		if !strict {
			bodyless = append(bodyless, key)
		}
	}

	t.Logf("validation sweep: %d mutating routes × %d probes = %d requests, no 5xx",
		len(keys), len(sweepProbes), len(keys)*len(sweepProbes))
	if len(bodyless) > 0 {
		// Not a failure: these handlers take no request body at all, so an
		// unknown member is nothing to reject. Listing them keeps the fact
		// visible — if one of them grows a body, it leaves this list.
		t.Logf("routes that read no request body (unknown members are ignored, not refused):\n  %s",
			strings.Join(bodyless, "\n  "))
	}
}

// orPattern is the concrete path for a case, defaulting to the pattern itself.
func orPattern(tc isoCase) string {
	if tc.path != "" {
		return tc.path
	}
	return tc.pattern
}

// sweepCaller picks the session a route is legitimately reached with, so the
// probe lands on the handler's own validation rather than bouncing off the
// middleware.
func sweepCaller(f *isoFixture, admin *client, h *harness, tc isoCase) (*client, string) {
	switch tc.aud {
	case isoOrg, isoParty:
		return f.ownerA, "org A owner"
	case isoRenter:
		return f.renter1, "renter 1"
	case isoAdmin:
		return admin, "platform admin"
	case isoOpen:
		return h.client(), "anonymous"
	default:
		return h.client(), "anonymous"
	}
}

// ------------------------------------------------- search-term escaping --

// TestSearchWildcardsAreLiteral is the Phase 7 carry-over: the `q` filters on
// `/units`, `/renters` and `/admin/audit-log` interpolate the term into
// `ILIKE '%' || q || '%'`, so an unescaped `%` or `_` turns a search into a
// wildcard nobody asked for — "show me everything" where the operator typed one
// character. Escaping is what makes a search for `_` find the rooms actually
// called that.
func TestSearchWildcardsAreLiteral(t *testing.T) {
	h := newHarness(t)
	fix := h.newOrgWithUnits("Wildcard", "wildcard@jjne.test", "0716003000",
		[]string{"Room 1", "Room 2", "100% Suite"}, testUnitAmount)

	cases := []struct {
		name  string
		query string
		want  []string // unit names the search must return, exactly
	}{
		{"a bare percent matches only the unit named with one", "%", []string{"100% Suite"}},
		{"a bare underscore matches nothing", "_", nil},
		{"a literal percent term still works", "100%", []string{"100% Suite"}},
		{"an ordinary term is unaffected", "Room", []string{"Room 1", "Room 2"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := listOf(t, fix.client.do(http.MethodGet,
				"/units?q="+url.QueryEscape(tc.query), nil).
				mustStatus(t, http.StatusOK, "search units"))

			var got []string
			for _, row := range rows {
				name, _ := row["name"].(string)
				got = append(got, name)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("q=%q returned %v, want %v — the wildcard was not escaped", tc.query, got, want)
			}
		})
	}

	// The renter directory shares the rule; with no renters in the org, a bare
	// wildcard must not conjure the platform's other tenants.
	renters := listOf(t, fix.client.do(http.MethodGet, "/renters?q=%25", nil).
		mustStatus(t, http.StatusOK, "search renters"))
	if len(renters) != 0 {
		t.Errorf("a `%%` renter search returned %d rows in an org with no renters", len(renters))
	}
}
