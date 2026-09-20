package httpserver

import (
	"net/http"
	"strings"
)

// CORS answers cross-origin browser requests for the origins in allowed.
//
// The apps normally share the API's origin (the dev proxy), and then this is
// a no-op: allowed is empty and no header is written. Deployed on their own
// domains (renter, landlord and admin apps on Vercel, the API on its own
// host) each app origin goes in CORS_ALLOWED_ORIGINS. Every allowed response
// carries Access-Control-Allow-Credentials so the httpOnly session cookie
// travels with fetch(..., {credentials: 'include'}); that in turn needs the
// cookie to be SameSite=None; Secure (COOKIE_SAME_SITE / COOKIE_SECURE).
//
// Preflights are answered here, before routing, so an OPTIONS on a route
// that only registers GET is not turned into a 405 by chi. The allowlist is
// exact-match on the Origin header; a wildcard is deliberately unsupported,
// since a credentialed response cannot use one anyway.
func CORS(allowed []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[strings.TrimRight(strings.TrimSpace(o), "/")] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		if len(set) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// The response differs by Origin whether or not it is allowed,
			// so caches must key on it either way.
			w.Header().Add("Vary", "Origin")
			if _, ok := set[origin]; !ok {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				reqHeaders := r.Header.Get("Access-Control-Request-Headers")
				if reqHeaders == "" {
					reqHeaders = "Content-Type, Accept"
				}
				h.Set("Access-Control-Allow-Headers", reqHeaders)
				h.Set("Access-Control-Max-Age", "600")
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// Response headers the apps read on the JS side.
			h.Set("Access-Control-Expose-Headers", "Content-Disposition, X-Request-Id")
			next.ServeHTTP(w, r)
		})
	}
}
