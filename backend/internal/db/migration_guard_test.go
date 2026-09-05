package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// migrationPath is the Phase 1 schema migration the guards are derived from.
const (
	migrationUp   = "../../migrations/000002_schema.up.sql"
	migrationDown = "../../migrations/000002_schema.down.sql"
)

// guardExemptTables carry an org_id column but are deliberately absent from
// orgScopedTables in orgscope_guard_test.go:
//
//	sessions — keyed by token hash. org_id is the payload of the session (what
//	the principal is scoped to), never a filter on the lookup.
var guardExemptTables = map[string]string{
	"sessions": "org_id is session payload, not a query filter",
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
	sql := readMigration(t, migrationUp)
	found := tablesWithOrgID(t, sql)
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
		t.Errorf("table %q carries org_id in 000002 but is missing from orgScopedTables — "+
			"add it there, or document an exemption in guardExemptTables", tbl)
	}

	// The reverse direction: no stale entries naming tables that no longer exist.
	inMigration := map[string]bool{}
	for _, tbl := range found {
		inMigration[tbl] = true
	}
	for _, tbl := range orgScopedTables {
		if !inMigration[tbl] {
			t.Errorf("orgScopedTables lists %q, which has no org_id column in 000002", tbl)
		}
	}
}

// TestDownMigrationReversesUp checks that every table and function created by
// 000002 is dropped by its down migration, so a rollback leaves no orphans.
func TestDownMigrationReversesUp(t *testing.T) {
	up := readMigration(t, migrationUp)
	down := readMigration(t, migrationDown)
	lowerDown := strings.ToLower(down)

	tables := createTableRe.FindAllStringSubmatch(up, -1)
	if len(tables) == 0 {
		t.Fatal("no CREATE TABLE statements parsed from the up migration")
	}
	for _, m := range tables {
		if !strings.Contains(lowerDown, "drop table if exists "+strings.ToLower(m[1])) {
			t.Errorf("down migration does not drop table %q", m[1])
		}
	}

	funcs := createFuncRe.FindAllStringSubmatch(up, -1)
	if len(funcs) == 0 {
		t.Fatal("no CREATE FUNCTION statements parsed from the up migration")
	}
	for _, m := range funcs {
		if !strings.Contains(lowerDown, "drop function if exists "+strings.ToLower(m[1])) {
			t.Errorf("down migration does not drop function %q", m[1])
		}
	}
}

// TestOrgScopedTablesAreIndexedOnOrgID enforces SPEC §2.1: every org-scoped
// table has an index whose leading column is org_id, because every query
// against it filters on org_id.
func TestOrgScopedTablesAreIndexedOnOrgID(t *testing.T) {
	sql := strings.ToLower(readMigration(t, migrationUp))
	for _, tbl := range orgScopedTables {
		idx := regexp.MustCompile(`create (?:unique )?index [a-z0-9_]+ on ` + tbl + ` \(org_id`)
		if !idx.MatchString(sql) {
			t.Errorf("table %q has no index leading with org_id", tbl)
		}
	}
}
