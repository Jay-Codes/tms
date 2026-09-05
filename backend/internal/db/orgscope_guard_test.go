package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// orgScopedTables are the SPEC §4 tables that carry org_id. Every read or write
// against one of them must be filtered by org_id: that filter is the whole of
// TMS's multi-tenant isolation (SPEC §2.1), so it is checked mechanically
// rather than left to review.
//
// Two exemptions exist, both deliberate:
//   - files named admin_*.sql (platform-admin endpoints, which cross orgs by
//     design and sit behind auth.RequireAdmin), and
//   - individual queries carrying a `-- guard-exempt: <reason>` comment.
var orgScopedTables = []string{
	"payment_periods",
	"org_branding",
	"org_members",
	"properties",
	"units",
	"price_plans",
	"contract_templates",
	"contracts",
	"contract_signatures",
	"unit_link_requests",
	"payment_schedules",
	"payments",
	"notification_log",
	"audit_log",
}

var (
	tableRefRe  = regexp.MustCompile(`(?i)\b(?:from|join|update|into)\s+([a-z_][a-z0-9_]*)`)
	nameRe      = regexp.MustCompile(`^--\s*name:\s*(\S+)`)
	exemptRe    = regexp.MustCompile(`^--\s*guard-exempt:\s*(\S.*)$`)
	readWriteRe = regexp.MustCompile(`(?i)^(select|update|delete)\b`)
)

type queryBlock struct {
	name       string
	body       string
	exemptions []string
}

func parseQueries(t *testing.T, path string) []queryBlock {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var (
		blocks  []queryBlock
		pending []string
		cur     *queryBlock
	)
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if m := exemptRe.FindStringSubmatch(trimmed); m != nil {
			pending = append(pending, m[1])
			continue
		}
		if m := nameRe.FindStringSubmatch(trimmed); m != nil {
			if cur != nil {
				blocks = append(blocks, *cur)
			}
			cur = &queryBlock{name: m[1], exemptions: pending}
			pending = nil
			continue
		}
		if cur != nil && !strings.HasPrefix(trimmed, "--") {
			cur.body += " " + trimmed
		}
	}
	if cur != nil {
		blocks = append(blocks, *cur)
	}
	return blocks
}

func referencesOrgScoped(body string) string {
	for _, m := range tableRefRe.FindAllStringSubmatch(body, -1) {
		for _, table := range orgScopedTables {
			if strings.EqualFold(m[1], table) {
				return table
			}
		}
	}
	return ""
}

// TestOrgScopedQueriesFilterByOrgID enforces the repository guard from
// SPEC §2.1: no SELECT/UPDATE/DELETE against an org-scoped table may exist
// without an `org_id =` predicate.
func TestOrgScopedQueriesFilterByOrgID(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("queries", "*.sql"))
	if err != nil {
		t.Fatalf("glob queries: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no query files found — the guard would pass vacuously")
	}

	checked := 0
	for _, file := range files {
		base := filepath.Base(file)
		if strings.HasPrefix(base, "admin_") {
			continue // platform-admin queries cross orgs by design
		}
		for _, b := range parseQueries(t, file) {
			body := strings.TrimSpace(b.body)
			if !readWriteRe.MatchString(body) {
				continue // INSERT and friends carry org_id in their column list
			}
			table := referencesOrgScoped(body)
			if table == "" {
				continue
			}
			checked++
			if len(b.exemptions) > 0 {
				for _, reason := range b.exemptions {
					if strings.TrimSpace(reason) == "" {
						t.Errorf("%s: query %s has an empty guard-exempt reason", base, b.name)
					}
				}
				continue
			}
			if !strings.Contains(strings.ToLower(body), "org_id =") {
				t.Errorf("%s: query %s reads/writes org-scoped table %q without an `org_id =` filter",
					base, b.name, table)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no org-scoped queries were checked — the guard is not doing anything")
	}
}

// TestGuardCatchesUnscopedQuery proves the guard actually fails on a bad query,
// so a future refactor cannot silently neuter it.
func TestGuardCatchesUnscopedQuery(t *testing.T) {
	body := "SELECT * FROM contracts WHERE id = $1"
	if referencesOrgScoped(body) == "" {
		t.Fatal("table reference detection missed `FROM contracts`")
	}
	if strings.Contains(strings.ToLower(body), "org_id =") {
		t.Fatal("the sample query should not look org-scoped")
	}
}
