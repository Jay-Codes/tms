package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"tms/backend/internal/db/sqlc"
)

// The platform catalogue is the middle layer of template resolution (PLAN2
// Phase 14): org override → DB platform template → the built-in Go default.
//
// The Go map in templates.go stays as the last fallback rather than being
// deleted. It is what a fresh database is seeded from, and it is what renders
// a message when Postgres is unreachable at the moment a send goes out — a
// renter should not miss a rent reminder because the wording table could not
// be read.

// PlatformCacheTTL is how long a resolved kind is cached, in Redis and in the
// process. Five minutes: an admin's edit is invalidated explicitly on save, so
// the TTL only bounds how stale another API instance can be.
const PlatformCacheTTL = 5 * time.Minute

// platformCacheKey is the Redis key one kind's wording is cached under.
func platformCacheKey(kind string) string { return "tmpl:" + kind }

// PlatformStore resolves a kind's platform wording from Postgres, cached in
// Redis and in-process.
//
// Every layer is optional. No Redis means the process cache and Postgres; no
// Postgres means the code defaults. The store never returns an error: the
// caller is a send path, and "no wording" is not an outcome an SMS can have.
type PlatformStore struct {
	Q      *sqlc.Queries
	Redis  *redis.Client
	Logger *slog.Logger
	// TTL overrides PlatformCacheTTL (tests).
	TTL time.Duration

	mu     sync.RWMutex
	local  map[string]platformEntry
	locked lockedEntry
}

type platformEntry struct {
	tpl Template
	at  time.Time
	// present is false for a kind the table does not carry, which is cached
	// too: a miss must not become a database round trip per send.
	present bool
}

type lockedEntry struct {
	kinds map[string]bool
	at    time.Time
}

func (s *PlatformStore) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return PlatformCacheTTL
}

func (s *PlatformStore) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Get returns the platform wording for one kind and whether the catalogue
// carries it.
func (s *PlatformStore) Get(ctx context.Context, kind string) (Template, bool) {
	if s == nil {
		return Template{}, false
	}
	now := time.Now()

	s.mu.RLock()
	e, ok := s.local[kind]
	s.mu.RUnlock()
	if ok && now.Sub(e.at) < s.ttl() {
		return e.tpl, e.present
	}

	if s.Redis != nil {
		if raw, err := s.Redis.Get(ctx, platformCacheKey(kind)).Result(); err == nil {
			var t Template
			if json.Unmarshal([]byte(raw), &t) == nil {
				s.remember(kind, t, true, now)
				return t, true
			}
		}
	}

	if s.Q == nil {
		return Template{}, false
	}
	row, err := s.Q.GetPlatformTemplate(ctx, kind)
	if err != nil {
		if err != pgx.ErrNoRows {
			s.logger().Warn("platform template lookup failed; using the code default",
				"kind", kind, "error", err)
			// Not cached: a transient database failure must not pin the code
			// default in front of an admin's wording for five minutes.
			return Template{}, false
		}
		s.remember(kind, Template{}, false, now)
		return Template{}, false
	}
	t := Template{SW: row.Sw, EN: row.En}
	s.remember(kind, t, true, now)
	if s.Redis != nil {
		if blob, mErr := json.Marshal(t); mErr == nil {
			if setErr := s.Redis.Set(ctx, platformCacheKey(kind), blob, s.ttl()).Err(); setErr != nil {
				s.logger().Debug("platform template cache write failed", "kind", kind, "error", setErr)
			}
		}
	}
	return t, true
}

func (s *PlatformStore) remember(kind string, t Template, present bool, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.local == nil {
		s.local = map[string]platformEntry{}
	}
	s.local[kind] = platformEntry{tpl: t, at: at, present: present}
}

// Invalidate drops one kind from both caches. It is called on every save, so
// the next render — in this process or another — reads the new wording rather
// than waiting out the TTL.
func (s *PlatformStore) Invalidate(ctx context.Context, kind string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.local, kind)
	s.locked = lockedEntry{}
	s.mu.Unlock()
	if s.Redis != nil {
		if err := s.Redis.Del(ctx, platformCacheKey(kind)).Err(); err != nil {
			s.logger().Warn("platform template cache invalidation failed", "kind", kind, "error", err)
		}
	}
}

// LockedKinds returns the kinds an org may not override. A database failure
// answers with what is cached, and an empty set when there is nothing cached:
// a locked kind is a restriction, and failing open is the behaviour that keeps
// the landlord's settings screen working during an outage. `otp` is never
// overridable regardless — it is not in TemplateKinds at all.
func (s *PlatformStore) LockedKinds(ctx context.Context) map[string]bool {
	if s == nil {
		return map[string]bool{}
	}
	s.mu.RLock()
	cached := s.locked
	s.mu.RUnlock()
	if cached.kinds != nil && time.Since(cached.at) < s.ttl() {
		return cached.kinds
	}
	if s.Q == nil {
		return map[string]bool{}
	}
	kinds, err := s.Q.ListLockedTemplateKinds(ctx)
	if err != nil {
		s.logger().Warn("locked template kinds lookup failed", "error", err)
		if cached.kinds != nil {
			return cached.kinds
		}
		return map[string]bool{}
	}
	out := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		out[k] = true
	}
	s.mu.Lock()
	s.locked = lockedEntry{kinds: out, at: time.Now()}
	s.mu.Unlock()
	return out
}

// ---------------------------------------------------- the process-wide store --

// activeStore is the store Render consults. It is a package variable because
// Render is called from a dozen places that have no business threading a
// catalogue handle through themselves, and there is exactly one catalogue.
//
//nolint:gochecknoglobals // one process, one platform catalogue.
var (
	activeMu    sync.RWMutex
	activeStore *PlatformStore
)

// UsePlatformStore installs the catalogue Render resolves against. cmd/api and
// the tests call it once at startup; passing nil restores the code defaults.
func UsePlatformStore(s *PlatformStore) {
	activeMu.Lock()
	activeStore = s
	activeMu.Unlock()
}

// ActivePlatformStore returns the installed catalogue, or nil.
func ActivePlatformStore() *PlatformStore {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return activeStore
}

// platformBody resolves one kind's wording in one language through the
// installed catalogue, falling back to the code default.
func platformBody(kind, lang string) string {
	if s := ActivePlatformStore(); s != nil {
		// Render carries no context: it is called from handlers, the
		// scheduler and the worker alike, and threading one through every
		// caller buys nothing here — the lookup is a cache hit in the ordinary
		// case and is bounded below.
		ctx, cancel := context.WithTimeout(context.Background(), platformLookupTimeout)
		defer cancel()
		if t, ok := s.Get(ctx, kind); ok {
			if body := strings.TrimSpace(t.pick(lang)); body != "" {
				return body
			}
		}
	}
	t, ok := platformTemplates[kind]
	if !ok {
		return ""
	}
	return t.pick(lang)
}

// platformLookupTimeout bounds one catalogue lookup. A wording table that has
// gone away must not hold up a send.
const platformLookupTimeout = 2 * time.Second

// ------------------------------------------------------------------ seeding --

// PlatformDefault returns the built-in wording for one kind — what the table
// is seeded from and what renders when the table cannot be read.
func PlatformDefault(kind string) (Template, bool) {
	t, ok := platformTemplates[kind]
	return t, ok
}

// SeedKinds lists every kind the platform catalogue carries, sorted. It is
// TemplateKinds plus `otp`: the admin edits the verification-code wording even
// though a landlord may not.
func SeedKinds() []string {
	out := make([]string, 0, len(platformTemplates))
	for k := range platformTemplates {
		if k == thankYouSettled {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// VariablesFor lists, sorted, the placeholders a kind's wording may name.
//
// It is derived from the platform default rather than written down twice: the
// variables a kind can carry are exactly the ones the platform sentence uses,
// plus the eight an org may use anywhere. `otp` is the exception — `{{code}}`
// is the only thing its message may say, and offering it `{{amount}}` would
// invite an admin to write a sentence the auth path cannot fill in.
func VariablesFor(kind string) []string {
	seen := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			seen[n] = true
		}
	}
	if t, ok := platformTemplates[kind]; ok {
		add(placeholdersIn(t.SW))
		add(placeholdersIn(t.EN))
	}
	if kind == KindThankYou {
		if t, ok := platformTemplates[thankYouSettled]; ok {
			add(placeholdersIn(t.SW))
			add(placeholdersIn(t.EN))
		}
	}
	if kind != KindOTP {
		add(OrgVariables)
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// placeholdersIn returns the `{{…}}` names a body uses.
func placeholdersIn(body string) []string {
	var out []string
	rest := body
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			return out
		}
		rest = rest[open+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(rest[:end]))
		rest = rest[end+2:]
	}
}

// SeedPlatformTemplates writes the code catalogue into platform_templates for
// any kind the table does not already carry.
//
// It runs on every startup, not only on a fresh install: a kind added to the
// code in a later phase has to reach the table without a migration, and an
// admin's existing wording is never touched (ON CONFLICT DO NOTHING).
func SeedPlatformTemplates(ctx context.Context, q *sqlc.Queries) error {
	if q == nil {
		return nil
	}
	for _, kind := range SeedKinds() {
		t, ok := platformTemplates[kind]
		if !ok {
			continue
		}
		if err := q.SeedPlatformTemplate(ctx, sqlc.SeedPlatformTemplateParams{
			Kind:      kind,
			Sw:        t.SW,
			En:        t.EN,
			Variables: VariablesFor(kind),
			// `otp` ships locked: a landlord rewording the message that lets
			// somebody into their account is a phishing surface (PLAN2).
			Locked: kind == KindOTP,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Reset drops every cached kind. It exists for the test harness, which
// truncates the catalogue between tests: a process cache that outlived the
// rows it describes would make one test's edit visible to the next.
func (s *PlatformStore) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.local = nil
	s.locked = lockedEntry{}
	s.mu.Unlock()
}
