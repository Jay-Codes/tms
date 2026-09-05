// Command proxy is the single edge in front of TMS: it routes /enduser,
// /tenant and /admin to the three Next.js servers, /api to the Go API, and the
// four bucket prefixes to MinIO, so one origin (an ngrok tunnel in dev, a TLS
// listener in a compose deploy) exposes the whole system.
//
// Everything is env-driven and every default is the dev loop, so running the
// binary with an empty environment behaves exactly as it did before Phase 8:
// host processes on localhost, plain HTTP on :8080.
//
//	UPSTREAM_ENDUSER   http://localhost:3001
//	UPSTREAM_TENANT    http://localhost:3002
//	UPSTREAM_ADMIN     http://localhost:3003
//	UPSTREAM_API       http://localhost:8081
//	UPSTREAM_MINIO     http://localhost:9000
//	PROXY_HTTP_ADDR    :8080
//	PROXY_HTTPS_ADDR   :443
//	TLS_CERT_FILE      (unset)
//	TLS_KEY_FILE       (unset)
//
// With TLS_CERT_FILE and TLS_KEY_FILE both set the proxy serves HTTPS on
// PROXY_HTTPS_ADDR and the HTTP listener becomes a 308 redirect to it;
// otherwise it serves plain HTTP only.
package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func mustProxy(target string) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("bad upstream %q: %v", target, err)
	}
	return httputil.NewSingleHostReverseProxy(u)
}

// routes builds the prefix table from the environment.
func routes() map[string]*httputil.ReverseProxy {
	minioProxy := mustProxy(getenv("UPSTREAM_MINIO", "http://localhost:9000"))
	return map[string]*httputil.ReverseProxy{
		"/enduser": mustProxy(getenv("UPSTREAM_ENDUSER", "http://localhost:3001")),
		"/tenant":  mustProxy(getenv("UPSTREAM_TENANT", "http://localhost:3002")),
		"/admin":   mustProxy(getenv("UPSTREAM_ADMIN", "http://localhost:3003")),
		// Go REST API. The backend serves under /api/v1 itself, so the path
		// is forwarded unchanged.
		"/api": mustProxy(getenv("UPSTREAM_API", "http://localhost:8081")),
		// MinIO buckets. Presigned URLs are signed against the public origin
		// (MINIO_PUBLIC_URL), so the path and the Host header must both reach
		// MinIO unchanged or the V4 signature will not validate.
		// httputil.NewSingleHostReverseProxy's director rewrites only
		// req.URL.{Scheme,Host}; it leaves req.Host alone, so the inbound Host
		// is forwarded as-is.
		"/branding":   minioProxy,
		"/qrcodes":    minioProxy,
		"/kyc":        minioProxy,
		"/signatures": minioProxy,
		"/receipts":   minioProxy,
	}
}

func handler() http.Handler {
	table := routes()
	mux := http.NewServeMux()

	// Liveness for the container healthcheck. Deliberately answered by the
	// proxy itself: it must report the edge being up, not an upstream.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		for prefix, p := range table {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				p.ServeHTTP(w, r)
				return
			}
		}
		// Next.js assets (HMR, /_next/*) are requested from the page's
		// basePath, so they carry the prefix already. Anything else gets a
		// landing page.
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><title>TMS</title>
<body style="font-family:system-ui;display:flex;flex-direction:column;align-items:center;justify-content:center;min-height:100vh;gap:1rem">
<h1>TMS</h1>
<a href="/enduser">End User</a>
<a href="/tenant">Tenant</a>
<a href="/admin">Admin</a>
</body>`)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

// redirectToHTTPS answers every request with a 308 to the same path on https.
// The port is only appended when the TLS listener is not on 443, so the common
// deploy produces clean URLs.
func redirectToHTTPS(httpsAddr string) http.Handler {
	_, port, err := net.SplitHostPort(httpsAddr)
	if err != nil {
		port = "443"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The container healthcheck talks plain HTTP to the loopback; it must
		// not be bounced to a TLS port whose cert it cannot verify.
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, "ok")
			return
		}
		host := r.Host
		if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			host = h
		}
		if port != "443" {
			host = net.JoinHostPort(host, port)
		}
		target := "https://" + host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

func main() {
	httpAddr := getenv("PROXY_HTTP_ADDR", ":8080")
	httpsAddr := getenv("PROXY_HTTPS_ADDR", ":443")
	certFile := getenv("TLS_CERT_FILE", "")
	keyFile := getenv("TLS_KEY_FILE", "")

	h := handler()

	if certFile == "" || keyFile == "" {
		log.Printf("proxy listening on %s (plain http)", httpAddr)
		log.Fatal(http.ListenAndServe(httpAddr, h))
	}

	// TLS mode: HTTPS serves the app, HTTP only redirects.
	go func() {
		log.Printf("proxy listening on %s (redirect to https)", httpAddr)
		if err := http.ListenAndServe(httpAddr, redirectToHTTPS(httpsAddr)); err != nil {
			log.Printf("http redirect listener stopped: %v", err)
		}
	}()

	srv := &http.Server{
		Addr:      httpsAddr,
		Handler:   h,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	log.Printf("proxy listening on %s (tls, cert %s)", httpsAddr, certFile)
	log.Fatal(srv.ListenAndServeTLS(certFile, keyFile))
}
