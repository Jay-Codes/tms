// Package expense holds the parts of the expense ledger that are not HTTP: the
// seeded category vocabulary every org starts with, and the grouping and
// comparison arithmetic behind GET /expenses/summary.
//
// The arithmetic lives here rather than in the handler because it is the one
// part of the feature with an answer that can be wrong rather than merely
// missing — a total that disagrees with the list it summarises, or a "+12%"
// measured against nothing at all — and it is table-tested apart from the
// database (the shape internal/payment.Allocate set in Phase 5).
package expense

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"tms/backend/internal/db/sqlc"
	"tms/backend/internal/report"
)

// DefaultCategories are the eight categories every org is bootstrapped with
// (PLAN2 Phase 10, FLOWS 12.5). They are seeded rather than hard-coded as an
// enum because "Utilities" means what each landlord decides it means: the org
// may rename, reorder or deactivate any of them.
//
// The order is the seeded sort_order (1…8), and it is deliberate: the four a
// Tanzanian landlord touches monthly come first, and "Other" is last because a
// list that opens on its escape hatch teaches people to use it.
//
//nolint:gochecknoglobals // fixed product data, not configuration.
var DefaultCategories = []string{
	"Repairs & maintenance",
	"Utilities",
	"Security",
	"Cleaning",
	"Taxes & levies",
	"Insurance",
	"Management fees",
	"Other",
}

// Statuses an expense row may hold. `voided` is a correction, not a deletion:
// the row stays and is excluded from every total (FLOWS 12.3).
const (
	StatusRecorded = "recorded"
	StatusVoided   = "voided"
)

// Group is one bar of the summary: a property or a category, what was spent
// under it in the window, and how many rows that was.
type Group struct {
	// ID is nil for the "Uncategorised" bucket — the rows filed under no
	// category, which is not itself a category.
	ID     *string `json:"id"`
	Name   string  `json:"name"`
	Amount int64   `json:"amount"`
	Count  int64   `json:"count"`
}

// Total is a window's grand total.
type Total struct {
	Amount int64 `json:"amount"`
	Count  int64 `json:"count"`
}

// SortGroups orders the summary the way a chart reads it: biggest spend first,
// ties broken by name so the order is stable across two identical windows
// rather than left to the database's row order.
func SortGroups(groups []Group) {
	// A hand-rolled insertion sort keeps the package free of a sort.Slice
	// closure over a captured slice, and the input is at most a few dozen
	// properties or eight categories.
	for i := 1; i < len(groups); i++ {
		g := groups[i]
		j := i - 1
		for j >= 0 && less(g, groups[j]) {
			groups[j+1] = groups[j]
			j--
		}
		groups[j+1] = g
	}
}

func less(a, b Group) bool {
	if a.Amount != b.Amount {
		return a.Amount > b.Amount
	}
	return a.Name < b.Name
}

// ChangePct is the movement from the previous window to this one, as a
// percentage rounded to one decimal place.
//
// It is nil when the previous window is empty: a rise from nothing is not
// "+100%", it is a first month, and quoting a number there would be inventing
// one. A fall to nothing from a non-empty previous window is -100%, which is
// exactly what happened.
// The arithmetic itself is internal/report's, shared with the Phase 11 series
// so a "-8.4%" on the expenses card and a "-8.4%" on the revenue chart are the
// same computation rather than two that happen to agree today.
func ChangePct(current, previous int64) *float64 {
	return report.ChangePct(current, previous)
}

// SeedCategories writes the eight defaults for an org using the supplied
// queries handle (pass a transaction-bound one).
//
// It is called from three places — the org bootstrap, the seeder, and lazily on
// the first read of an org that has none — because an org created before
// Phase 10 shipped has no categories at all, and a picker with nothing in it
// would make the feature look broken to exactly the landlords who have been
// using the product longest.
//
// A unique violation is swallowed: two concurrent first reads are the only way
// to reach one, and the loser's work was already done by the winner.
func SeedCategories(ctx context.Context, q *sqlc.Queries, orgID pgtype.UUID) error {
	for i, name := range DefaultCategories {
		_, err := q.CreateExpenseCategory(ctx, sqlc.CreateExpenseCategoryParams{
			OrgID: orgID, Name: name, IsDefault: true, SortOrder: int32(i + 1),
		})
		if err != nil && !isUniqueViolation(err) {
			return fmt.Errorf("expense: seed category %q: %w", name, err)
		}
	}
	return nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint breach
// (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
