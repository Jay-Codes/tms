// Dev reverse proxy: routes /enduser, /tenant, /admin to the three
// Next.js dev servers so a single ngrok tunnel can expose all of them.
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

func mustProxy(target string) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatal(err)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	return p
}

func main() {
	routes := map[string]*httputil.ReverseProxy{
		"/enduser": mustProxy("http://localhost:3001"),
		"/tenant":  mustProxy("http://localhost:3002"),
		"/admin":   mustProxy("http://localhost:3003"),
		// Go REST API. The backend serves under /api/v1 itself, so the path
		// is forwarded unchanged.
		"/api": mustProxy("http://localhost:8081"),
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		for prefix, p := range routes {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				p.ServeHTTP(w, r)
				return
			}
		}
		// Next.js dev assets (HMR, /_next/*) are requested from the page's
		// basePath, so they carry the prefix already. Anything else gets a
		// landing page.
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><title>TMS Dev</title>
<body style="font-family:system-ui;display:flex;flex-direction:column;align-items:center;justify-content:center;min-height:100vh;gap:1rem">
<h1>TMS Dev Preview</h1>
<a href="/enduser">End User</a>
<a href="/tenant">Tenant</a>
<a href="/admin">Admin</a>
</body>`)
			return
		}
		http.NotFound(w, r)
	})

	log.Println("dev proxy listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
