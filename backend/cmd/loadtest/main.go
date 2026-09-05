// Command loadtest is the Phase 8 load pass: it logs in as the load-test org
// owner and hammers the hot read endpoints with a fixed pool of workers for a
// fixed duration, then prints p50 / p95 / p99, throughput and errors per
// endpoint.
//
// It is deliberately dependency-free (net/http and the standard library only)
// so the load pass runs anywhere `go run` does — no k6, no vegeta, no wrk.
//
// Usage:
//
//	loadtest
//	loadtest -base http://localhost:8081 -duration 30s -workers 40
//
// Note on the public endpoint: GET /public/units/{code} is rate-limited to 60
// requests a minute per client IP. Every worker therefore presents its own
// X-Forwarded-For — which loopback callers are trusted to set
// (TRUSTED_PROXY_CIDRS) — so the pass measures the handler rather than the
// limiter. Any 429 that still lands is counted and reported separately.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type endpoint struct {
	name string
	path string
	// public endpoints are called without the session cookie.
	public bool
}

// stats accumulates one endpoint's latencies. Every worker keeps its own and
// they are merged at the end, so no lock sits on the hot path.
type stats struct {
	latencies []time.Duration
	ok        int
	errs      int
	throttled int
	statuses  map[int]int
}

func newStats() *stats { return &stats{statuses: map[int]int{}} }

func (s *stats) add(d time.Duration, code int, err error) {
	// A throttled request is the limiter answering, not the handler; counting
	// its (very fast) latency would flatter every percentile.
	if code != http.StatusTooManyRequests {
		s.latencies = append(s.latencies, d)
	}
	switch {
	case err != nil:
		s.errs++
	case code == http.StatusTooManyRequests:
		s.throttled++
		s.statuses[code]++
	case code >= 200 && code < 300:
		s.ok++
		s.statuses[code]++
	default:
		s.errs++
		s.statuses[code]++
	}
}

func (s *stats) merge(o *stats) {
	s.latencies = append(s.latencies, o.latencies...)
	s.ok += o.ok
	s.errs += o.errs
	s.throttled += o.throttled
	for k, v := range o.statuses {
		s.statuses[k] += v
	}
}

func (s *stats) percentile(p float64) time.Duration {
	if len(s.latencies) == 0 {
		return 0
	}
	idx := int(p * float64(len(s.latencies)-1))
	return s.latencies[idx]
}

func main() {
	var (
		base     = flag.String("base", envOr("LOADTEST_BASE", "http://localhost:8080"), "API origin (the dev proxy, or the API directly)")
		email    = flag.String("email", "load@tms.local", "org owner email")
		password = flag.String("password", "password123", "org owner password")
		duration = flag.Duration("duration", 20*time.Second, "how long to run")
		workers  = flag.Int("workers", 20, "concurrent workers")
		target   = flag.Duration("target-p95", 300*time.Millisecond, "p95 budget; a breach exits non-zero")
	)
	flag.Parse()

	prefix := strings.TrimRight(*base, "/") + "/api/v1"

	jar, err := cookiejar.New(nil)
	if err != nil {
		fail("cookie jar: %v", err)
	}
	login := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	if err := doLogin(login, prefix, *email, *password); err != nil {
		fail("login as %s failed: %v\n"+
			"       run `make seed` first, or pass -email/-password", *email, err)
	}

	unitCode, err := firstUnitCode(login, prefix)
	if err != nil {
		fail("could not resolve a unit code for the public endpoint: %v", err)
	}

	endpoints := []endpoint{
		{name: "GET /units?status=vacant", path: "/units?status=vacant&limit=50"},
		{name: "GET /schedules?status=overdue", path: "/schedules?status=overdue&limit=50"},
		{name: "GET /reports/summary", path: "/reports/summary"},
		{name: "GET /public/units/{code}", path: "/public/units/" + unitCode, public: true},
		{name: "GET /contracts", path: "/contracts?limit=50"},
	}

	// One shared transport (so connection reuse is realistic) behind two
	// clients: the authenticated one carries the session cookie jar, the
	// public one deliberately carries nothing.
	transport := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		MaxConnsPerHost:     200,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second, Transport: transport}
	anon := &http.Client{Timeout: 20 * time.Second, Transport: transport}

	fmt.Printf("load pass: %s, %d workers, %s, unit %s\n", prefix, *workers, *duration, unitCode)

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	merged := make([]*stats, len(endpoints))
	for i := range merged {
		merged[i] = newStats()
	}
	var mu sync.Mutex
	var wg sync.WaitGroup

	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			local := make([]*stats, len(endpoints))
			for i := range local {
				local[i] = newStats()
			}
			// The public route is rate-limited per client IP (60/min), which is
			// the right behaviour for one phone and the wrong thing to measure
			// here: the pass is about the handler under concurrency, and real
			// QR traffic arrives from thousands of distinct handsets. Each
			// request therefore presents its own address — loopback callers are
			// trusted to set X-Forwarded-For (TRUSTED_PROXY_CIDRS).
			rng := rand.New(rand.NewSource(int64(id)*7919 + 13))
			seq := 0

			for ctx.Err() == nil {
				i := rng.Intn(len(endpoints))
				e := endpoints[i]
				hc := client
				if e.public {
					hc = anon
				}
				seq++
				clientIP := fmt.Sprintf("10.%d.%d.%d", 1+(seq/65000)%200, (seq/250)%256, 1+seq%250)
				d, code, err := hit(ctx, hc, prefix+e.path, clientIP)
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					break
				}
				local[i].add(d, code, err)
			}

			mu.Lock()
			for i := range local {
				merged[i].merge(local[i])
			}
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)

	report(endpoints, merged, elapsed, *target)
}

// hit performs one request and returns how long it took.
func hit(ctx context.Context, c *http.Client, url, clientIP string) (time.Duration, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("X-Forwarded-For", clientIP)
	req.Header.Set("Accept", "application/json")

	started := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		return time.Since(started), 0, err
	}
	// Drain so the connection returns to the pool; the body is part of the
	// latency a real client sees.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return time.Since(started), resp.StatusCode, nil
}

func report(endpoints []endpoint, merged []*stats, elapsed time.Duration, target time.Duration) {
	fmt.Printf("\nelapsed %s\n\n", elapsed.Round(time.Millisecond))
	header := fmt.Sprintf("%-32s %8s %9s %9s %9s %9s %8s %8s %6s",
		"endpoint", "reqs", "p50", "p95", "p99", "max", "rps", "errors", "429s")
	fmt.Println(header)
	fmt.Println(strings.Repeat("-", len(header)))

	breached := false
	totalReqs := 0
	for i, e := range endpoints {
		s := merged[i]
		sort.Slice(s.latencies, func(a, b int) bool { return s.latencies[a] < s.latencies[b] })
		n := len(s.latencies)
		totalReqs += n
		rps := float64(n) / elapsed.Seconds()
		p95 := s.percentile(0.95)
		flag := ""
		if p95 > target && s.ok > 0 {
			flag = "  <-- over budget"
			breached = true
		}
		fmt.Printf("%-32s %8d %9s %9s %9s %9s %8.1f %8d %6d%s\n",
			e.name, n,
			ms(s.percentile(0.50)), ms(p95), ms(s.percentile(0.99)), ms(s.percentile(1.0)),
			rps, s.errs, s.throttled, flag)
	}
	fmt.Printf("\ntotal %d requests, %.1f rps aggregate, p95 budget %s\n",
		totalReqs, float64(totalReqs)/elapsed.Seconds(), target)

	for i, e := range endpoints {
		if len(merged[i].statuses) > 1 || merged[i].errs > 0 {
			fmt.Printf("  %s statuses: %v\n", e.name, merged[i].statuses)
		}
	}
	if breached {
		fmt.Println("\nRESULT: p95 budget breached — see docs/LOADTEST.md")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: every endpoint inside the p95 budget")
}

func ms(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}

func doLogin(c *http.Client, prefix, email, password string) error {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := c.Post(prefix+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

// firstUnitCode picks a unit from the authenticated org to exercise the public
// endpoint against.
func firstUnitCode(c *http.Client, prefix string) (string, error) {
	resp, err := c.Get(prefix + "/units?limit=1")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET /units → %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Items []struct {
			UnitCode string `json:"unit_code"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Items) == 0 || out.Items[0].UnitCode == "" {
		return "", fmt.Errorf("the org has no units — run `make seed` first")
	}
	return out.Items[0].UnitCode, nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "loadtest: "+format+"\n", args...)
	os.Exit(1)
}
