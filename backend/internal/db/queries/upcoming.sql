-- Phase 16 §16.3: due-date visibility (PLAN2 Phase 16, API.md Part 2 — Phase 16).
--
-- One rule holds across every query here and the Phase 7 report beside it: a
-- row is "still owed" when its status is pending, partial or overdue, and what
-- it owes is `GREATEST(amount - paid_amount, 0)`. `waived` and `paid` rows are
-- not money anybody is waiting for, and a contract that has ended or been
-- terminated is not a tenancy the landlord is chasing.
--
-- The window is a pair of calendar dates already resolved on the Dar es Salaam
-- wall clock by the handler (internal/tz): the database is never asked what
-- "today" is, because two callers on either side of 21:00 UTC would get
-- different answers to that question.

-- ListUpcomingSchedules is GET /reports/upcoming: everything still owed with a
-- due date inside the window, oldest first, with the renter and the unit it
-- belongs to so the row reads without a second call.
-- name: ListUpcomingSchedules :many
SELECT s.id, s.contract_id, s.period_start, s.period_end, s.due_date,
       s.amount, s.paid_amount, s.status,
       c.renter_user_id,
       ru.full_name AS renter_name, ru.phone AS renter_phone,
       u.name AS unit_name, pr.name AS property_name
FROM payment_schedules s
JOIN contracts c   ON c.id = s.contract_id AND c.org_id = s.org_id
JOIN units u       ON u.id = c.unit_id AND u.org_id = s.org_id
JOIN properties pr ON pr.id = u.property_id AND pr.org_id = s.org_id
JOIN users ru      ON ru.id = c.renter_user_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL
  AND c.deleted_at IS NULL AND c.status IN ('active', 'expiring')
  AND s.status IN ('pending', 'partial', 'overdue')
  AND s.due_date >= sqlc.arg(from_date) AND s.due_date <= sqlc.arg(to_date)
  AND (sqlc.narg(property_id)::uuid IS NULL OR pr.id = sqlc.narg(property_id)::uuid)
ORDER BY s.due_date, u.name, s.id;

-- UpcomingTotals is the same window as one aggregate, for the dashboard card:
-- the landlord's home screen wants "8 rows, TZS 2,400,000" and must not have to
-- download the rows to add them up.
-- name: UpcomingTotals :one
SELECT count(*)::bigint AS row_count,
       COALESCE(sum(GREATEST(s.amount - s.paid_amount, 0)), 0)::bigint AS total_due
FROM payment_schedules s
JOIN contracts c ON c.id = s.contract_id AND c.org_id = s.org_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL
  AND c.deleted_at IS NULL AND c.status IN ('active', 'expiring')
  AND s.status IN ('pending', 'partial', 'overdue')
  AND s.due_date >= sqlc.arg(from_date) AND s.due_date <= sqlc.arg(to_date);

-- ListUnitNextDue is the units board chip: for each unit with a running
-- tenancy, the next date money is owed on it. Units with nothing outstanding
-- are absent rather than null — the handler leaves their chip empty.
-- name: ListUnitNextDue :many
SELECT DISTINCT ON (c.unit_id)
       c.unit_id, s.due_date
FROM payment_schedules s
JOIN contracts c ON c.id = s.contract_id AND c.org_id = s.org_id
WHERE s.org_id = sqlc.arg(org_id) AND s.deleted_at IS NULL
  AND c.deleted_at IS NULL AND c.status IN ('active', 'expiring')
  AND s.status IN ('pending', 'partial', 'overdue')
ORDER BY c.unit_id, s.due_date, s.id;

-- ListMySubmittedProofsBySchedule is the "Awaiting confirmation" chip on the
-- renter's own screens (§16.3): the newest proof still awaiting a decision for
-- each instalment they have filed one against.
--
-- guard-exempt: renter scope. A proof belongs to the person who filed it and
-- the caller is that person; the org it concerns is the row's own fact, not a
-- filter the renter's session could supply (the same rule GET /me/payments and
-- GET /me/schedules follow).
-- name: ListMySubmittedProofsBySchedule :many
SELECT DISTINCT ON (p.schedule_id) p.schedule_id, p.id, p.status
FROM payment_proofs p
WHERE p.renter_user_id = sqlc.arg(renter_user_id)
  AND p.status = 'submitted' AND p.schedule_id IS NOT NULL
ORDER BY p.schedule_id, p.created_at DESC, p.id DESC;
