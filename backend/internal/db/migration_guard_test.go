package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// schemaMigrations are the migrations the guards are derived from: the Phase 1
// schema plus every later migration that creates a table. A migration that adds
// an org-scoped table must be listed here, or its table silently escapes the
// isolation guard.
//
//nolint:gochecknoglobals // fixed list of migration files, read-only.
var schemaMigrations = []struct{ up, down string }{
	{"../../migrations/000002_schema.up.sql", "../../migrations/000002_schema.down.sql"},
	{"../../migrations/000007_payments.up.sql", "../../migrations/000007_payments.down.sql"},
	{"../../migrations/000012_part2_foundations.up.sql", "../../migrations/000012_part2_foundations.down.sql"},
	{"../../migrations/000018_payment_proofs.up.sql", "../../migrations/000018_payment_proofs.down.sql"},
	{"../../migrations/000019_imports.up.sql", "../../migrations/000019_imports.down.sql"},
}

// guardExemptTables carry an org_id column but are deliberately absent from
// orgScopedTables in orgscope_guard_test.go:
//
//	sessions — keyed by token hash. org_id is the payload of the session (what
//	the principal is scoped to), never a filter on the lookup.
var guardExemptTables = map[string]string{
	"sessions": "org_id is session payload, not a query filter",
	// Part 2 tables whose org_id is the primary key: the row IS the org's
	// single record, so the PK index is the org_id index and every query is a
	// single-row lookup by it. They join orgScopedTables the day one of them
	// grows a second row per org.
	"org_themes":      "org_id is the primary key: one row per org, looked up by it",
	"org_sms_credits": "org_id is the primary key: one row per org, looked up by it",
	// The credit ledger is append-only and read only through the org-scoped
	// index below; it joins the guard proper in Phase 14, with its queries.
	"sms_credit_ledger": "Phase 14 table; no queries yet — its reads are added with the endpoints",
}

var (
	createTableRe = regexp.MustCompile(`(?im)^CREATE TABLE (?:IF NOT EXISTS )?([a-z_][a-z0-9_]*)\s*\(`)
	createFuncRe  = regexp.MustCompile(`(?im)^CREATE (?:OR REPLACE )?FUNCTION ([a-z_][a-z0-9_]*)\s*\(`)
	orgIDColRe    = regexp.MustCompile(`(?im)^\s*org_id\s`)
)

func readMigration(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// tablesWithOrgID splits the up migration into CREATE TABLE blocks and returns
// the tables carrying an org_id column.
func tablesWithOrgID(t *testing.T, sql string) []string {
	t.Helper()
	locs := createTableRe.FindAllStringSubmatchIndex(sql, -1)
	var out []string
	for i, loc := range locs {
		name := sql[loc[2]:loc[3]]
		end := len(sql)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := sql[loc[1]:end]
		if stop := strings.Index(body, "\n);"); stop >= 0 {
			body = body[:stop]
		}
		if orgIDColRe.MatchString(body) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// TestOrgScopedTableListMatchesMigration keeps the repository guard honest: a
// new org_id-carrying table added to the schema must either join
// orgScopedTables (and therefore be checked for an org_id filter) or be listed
// as a deliberate exemption here. Without this, adding a table silently opts it
// out of the isolation guard.
func TestOrgScopedTableListMatchesMigration(t *testing.T) {
	var found []string
	for _, m := range schemaMigrations {
		found = append(found, tablesWithOrgID(t, readMigration(t, m.up))...)
	}
	sort.Strings(found)
	if len(found) < 10 {
		t.Fatalf("only %d org_id tables parsed out of the migration — the parser is broken: %v", len(found), found)
	}

	guarded := map[string]bool{}
	for _, tbl := range orgScopedTables {
		guarded[tbl] = true
	}

	for _, tbl := range found {
		if guarded[tbl] {
			continue
		}
		if reason, ok := guardExemptTables[tbl]; ok {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("table %q is guard-exempt with an empty reason", tbl)
			}
			continue
		}
		t.Errorf("table %q carries org_id in the schema migrations but is missing from "+
			"orgScopedTables — add it there, or document an exemption in guardExemptTables", tbl)
	}

	// The reverse direction: no stale entries naming tables that no longer exist.
	inMigration := map[string]bool{}
	for _, tbl := range found {
		inMigration[tbl] = true
	}
	for _, tbl := range orgScopedTables {
		if !inMigration[tbl] {
			t.Errorf("orgScopedTables lists %q, which no schema migration creates with an org_id column", tbl)
		}
	}
}

// TestDownMigrationReversesUp checks that every table and function created by
// 000002 is dropped by its down migration, so a rollback leaves no orphans.
func TestDownMigrationReversesUp(t *testing.T) {
	total := 0
	for _, mig := range schemaMigrations {
		up := readMigration(t, mig.up)
		lowerDown := strings.ToLower(readMigration(t, mig.down))

		tables := createTableRe.FindAllStringSubmatch(up, -1)
		total += len(tables)
		for _, m := range tables {
			if !strings.Contains(lowerDown, "drop table if exists "+strings.ToLower(m[1])) {
				t.Errorf("%s does not drop table %q", mig.down, m[1])
			}
		}
		for _, m := range createFuncRe.FindAllStringSubmatch(up, -1) {
			if !strings.Contains(lowerDown, "drop function if exists "+strings.ToLower(m[1])) {
				t.Errorf("%s does not drop function %q", mig.down, m[1])
			}
		}
	}
	if total == 0 {
		t.Fatal("no CREATE TABLE statements parsed from the up migrations")
	}
}

// TestOrgScopedTablesAreIndexedOnOrgID enforces SPEC §2.1: every org-scoped
// table has an index whose leading column is org_id, because every query
// against it filters on org_id.
//
// A table keyed *by* the org (`org_id UUID PRIMARY KEY`, as org_themes is)
// satisfies this with the primary key's own index; a second index on the same
// column would be dead weight Postgres still has to maintain.
func TestOrgScopedTablesAreIndexedOnOrgID(t *testing.T) {
	var b strings.Builder
	for _, m := range schemaMigrations {
		b.WriteString(readMigration(t, m.up))
		b.WriteString("\n")
	}
	sql := strings.ToLower(b.String())
	for _, tbl := range orgScopedTables {
		idx := regexp.MustCompile(`create (?:unique )?index [a-z0-9_]+\s+on ` + tbl + `\s+\(org_id`)
		pk := regexp.MustCompile(`create table ` + tbl + `\s*\(\s*org_id\s+uuid\s+primary key`)
		if !idx.MatchString(sql) && !pk.MatchString(sql) {
			t.Errorf("table %q has no index leading with org_id", tbl)
		}
	}
}
