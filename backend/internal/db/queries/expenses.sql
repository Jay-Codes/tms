-- Expenses and their categories (SPEC §5.11, PLAN2 Phase 10, FLOWS 12).
--
-- An expense is money the landlord spent on a property — the other half of the
-- ledger the payments tables hold. Corrections work the way a payment reversal
-- does: the row stays and is marked `voided` with a reason, because a repair
-- that was recorded and then cancelled is part of the record.
--
-- Every window filter is half-open on `incurred_on` — `>= from_date` and
-- `< to_date` — so the handler resolves inclusive wire dates into an exclusive
-- upper bound once and every query below agrees on what "the period" means.

-- ------------------------------------------------------------- categories --

-- name: ListExpenseCategories :many
SELECT * FROM expense_categories
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL
ORDER BY sort_order, name, id;

-- name: CountExpenseCategories :one
SELECT count(*) FROM expense_categories
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL;

-- name: GetExpenseCategory :one
SELECT * FROM expense_categories
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: CreateExpenseCategory :one
INSERT INTO expense_categories (org_id, name, is_default, sort_order)
VALUES (sqlc.arg(org_id), sqlc.arg(name), sqlc.arg(is_default), sqlc.arg(sort_order))
RETURNING *;

-- name: MaxExpenseCategorySortOrder :one
SELECT COALESCE(MAX(sort_order), 0)::int AS max_sort_order
FROM expense_categories
WHERE org_id = sqlc.arg(org_id) AND deleted_at IS NULL;

-- name: UpdateExpenseCategory :one
UPDATE expense_categories
SET name       = COALESCE(sqlc.narg(name)::text, name),
    sort_order = COALESCE(sqlc.narg(sort_order)::int, sort_order),
    active     = COALESCE(sqlc.narg(active)::boolean, active)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteExpenseCategory :one
UPDATE expense_categories
SET deleted_at = now()
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- CountExpensesInCategory decides whether a category may be deleted: a
-- category some expense still points at is deactivated, never removed, or the
-- ledger would lose the word it was filed under.
-- name: CountExpensesInCategory :one
SELECT count(*) FROM expenses
WHERE org_id = sqlc.arg(org_id) AND category_id = sqlc.arg(category_id) AND deleted_at IS NULL;

-- ---------------------------------------------------------------- expenses --

-- name: CreateExpense :one
INSERT INTO expenses (
    org_id, property_id, unit_id, category_id, amount, incurred_on,
    vendor, reference, note, recorded_by_user_id
)
VALUES (
    sqlc.arg(org_id), sqlc.arg(property_id), sqlc.narg(unit_id), sqlc.narg(category_id),
    sqlc.arg(amount), sqlc.arg(incurred_on), sqlc.arg(vendor), sqlc.arg(reference),
    sqlc.arg(note), sqlc.narg(recorded_by_user_id)
)
RETURNING *;

-- name: GetExpense :one
SELECT e.*,
       p.name AS property_name,
       u.name AS unit_name,
       c.name AS category_name,
       rb.full_name AS recorded_by_name
FROM expenses e
JOIN properties p            ON p.id = e.property_id AND p.org_id = e.org_id
LEFT JOIN units u            ON u.id = e.unit_id AND u.org_id = e.org_id
LEFT JOIN expense_categories c ON c.id = e.category_id AND c.org_id = e.org_id
LEFT JOIN users rb           ON rb.id = e.recorded_by_user_id
WHERE e.org_id = sqlc.arg(org_id) AND e.id = sqlc.arg(id) AND e.deleted_at IS NULL;

-- ListExpenses is the ledger: newest incurred first, with the identity blocks
-- a table needs so a row renders without a second request. The cursor compares
-- the whole sort tuple, so a page boundary inside a day cannot repeat or drop
-- a row.
-- name: ListExpenses :many
SELECT e.*,
       p.name AS property_name,
       u.name AS unit_name,
       c.name AS category_name,
       rb.full_name AS recorded_by_name
FROM expenses e
JOIN properties p            ON p.id = e.property_id AND p.org_id = e.org_id
LEFT JOIN units u            ON u.id = e.unit_id AND u.org_id = e.org_id
LEFT JOIN expense_categories c ON c.id = e.category_id AND c.org_id = e.org_id
LEFT JOIN users rb           ON rb.id = e.recorded_by_user_id
WHERE e.org_id = sqlc.arg(org_id) AND e.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR e.status = sqlc.narg(status)::text)
  AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid)
  AND (sqlc.narg(unit_id)::uuid IS NULL OR e.unit_id = sqlc.narg(unit_id)::uuid)
  AND (sqlc.narg(category_id)::uuid IS NULL OR e.category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR e.incurred_on >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR e.incurred_on < sqlc.narg(to_date)::date)
  AND (sqlc.narg(q)::text IS NULL
       OR e.vendor ILIKE '%' || sqlc.narg(q)::text || '%'
       OR e.reference ILIKE '%' || sqlc.narg(q)::text || '%'
       OR e.note ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(cursor_incurred)::date IS NULL
       OR (e.incurred_on, e.created_at, e.id)
          < (sqlc.narg(cursor_incurred)::date, sqlc.narg(cursor_created)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY e.incurred_on DESC, e.created_at DESC, e.id DESC
LIMIT sqlc.arg(row_limit);

-- ListExpenseTotals answers "what does this filter add up to" over the whole
-- filtered set, not the page: a ledger whose total changed as you paged would
-- be worse than no total at all.
-- name: ListExpenseTotals :one
SELECT count(*)::bigint AS row_count, COALESCE(SUM(e.amount), 0)::bigint AS amount
FROM expenses e
WHERE e.org_id = sqlc.arg(org_id) AND e.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR e.status = sqlc.narg(status)::text)
  AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid)
  AND (sqlc.narg(unit_id)::uuid IS NULL OR e.unit_id = sqlc.narg(unit_id)::uuid)
  AND (sqlc.narg(category_id)::uuid IS NULL OR e.category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR e.incurred_on >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR e.incurred_on < sqlc.narg(to_date)::date)
  AND (sqlc.narg(q)::text IS NULL
       OR e.vendor ILIKE '%' || sqlc.narg(q)::text || '%'
       OR e.reference ILIKE '%' || sqlc.narg(q)::text || '%'
       OR e.note ILIKE '%' || sqlc.narg(q)::text || '%');

-- UpdateExpense edits a live row only. A voided expense finds no row here and
-- the handler answers 409 — a correction is not undone by an edit.
-- name: UpdateExpense :one
UPDATE expenses
SET property_id = COALESCE(sqlc.narg(property_id)::uuid, property_id),
    unit_id     = CASE WHEN sqlc.arg(clear_unit)::boolean THEN NULL
                       ELSE COALESCE(sqlc.narg(unit_id)::uuid, unit_id) END,
    category_id = CASE WHEN sqlc.arg(clear_category)::boolean THEN NULL
                       ELSE COALESCE(sqlc.narg(category_id)::uuid, category_id) END,
    amount      = COALESCE(sqlc.narg(amount)::bigint, amount),
    incurred_on = COALESCE(sqlc.narg(incurred_on)::date, incurred_on),
    vendor      = COALESCE(sqlc.narg(vendor)::text, vendor),
    reference   = COALESCE(sqlc.narg(reference)::text, reference),
    note        = COALESCE(sqlc.narg(note)::text, note)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND deleted_at IS NULL AND status = 'recorded'
RETURNING *;

-- name: VoidExpense :one
UPDATE expenses
SET status = 'voided', voided_at = now(), void_reason = sqlc.arg(void_reason)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
  AND deleted_at IS NULL AND status = 'recorded'
RETURNING *;

-- SetExpenseReceipt writes all three receipt columns together — the key, and
-- the type and size MinIO reported for the object under it — so a row can never
-- describe a receipt it does not have. Clearing passes NULL for all three.
-- name: SetExpenseReceipt :one
UPDATE expenses
SET receipt_object_key   = sqlc.narg(receipt_object_key),
    receipt_content_type = sqlc.narg(receipt_content_type),
    receipt_size         = sqlc.narg(receipt_size)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id) AND deleted_at IS NULL
RETURNING *;

-- ----------------------------------------------------------------- summary --

-- ExpenseSummaryByProperty drives the all-properties chart. It reads from
-- properties rather than from expenses so a property that spent nothing this
-- month is a bar at zero rather than a gap in the series.
-- name: ExpenseSummaryByProperty :many
SELECT p.id, p.name,
       COALESCE(SUM(e.amount), 0)::bigint AS amount,
       COUNT(e.id)::bigint AS row_count
FROM properties p
LEFT JOIN expenses e
       ON e.property_id = p.id AND e.org_id = p.org_id
      AND e.deleted_at IS NULL AND e.status = 'recorded'
      AND e.incurred_on >= sqlc.arg(from_date) AND e.incurred_on < sqlc.arg(to_date)
WHERE p.org_id = sqlc.arg(org_id) AND p.deleted_at IS NULL
  AND (sqlc.narg(property_id)::uuid IS NULL OR p.id = sqlc.narg(property_id)::uuid)
GROUP BY p.id, p.name;

-- ExpenseSummaryByCategory reads from the active categories for the same
-- reason: the eight seeded rows are the chart's fixed vocabulary.
-- name: ExpenseSummaryByCategory :many
SELECT c.id, c.name,
       COALESCE(SUM(e.amount), 0)::bigint AS amount,
       COUNT(e.id)::bigint AS row_count
FROM expense_categories c
LEFT JOIN expenses e
       ON e.category_id = c.id AND e.org_id = c.org_id
      AND e.deleted_at IS NULL AND e.status = 'recorded'
      AND e.incurred_on >= sqlc.arg(from_date) AND e.incurred_on < sqlc.arg(to_date)
      AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid)
WHERE c.org_id = sqlc.arg(org_id) AND c.deleted_at IS NULL AND c.active
GROUP BY c.id, c.name;

-- ExpenseUncategorisedTotal is the bucket for rows filed under no category. It
-- is not a category, so it cannot come out of the query above.
-- name: ExpenseUncategorisedTotal :one
SELECT COALESCE(SUM(e.amount), 0)::bigint AS amount, COUNT(e.id)::bigint AS row_count
FROM expenses e
WHERE e.org_id = sqlc.arg(org_id) AND e.deleted_at IS NULL AND e.status = 'recorded'
  AND e.category_id IS NULL
  AND e.incurred_on >= sqlc.arg(from_date) AND e.incurred_on < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid);

-- ExpenseWindowTotal is the grand total of a window, and the same query run on
-- the previous window is what `change_pct` is measured against.
-- name: ExpenseWindowTotal :one
SELECT COALESCE(SUM(e.amount), 0)::bigint AS amount, COUNT(e.id)::bigint AS row_count
FROM expenses e
WHERE e.org_id = sqlc.arg(org_id) AND e.deleted_at IS NULL AND e.status = 'recorded'
  AND e.incurred_on >= sqlc.arg(from_date) AND e.incurred_on < sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR e.property_id = sqlc.narg(property_id)::uuid);
