-- Phase 8: hardening.
--
-- `GET /admin/audit-log` is the one audit read with no org in its WHERE clause:
-- an operator searching the whole platform pages by `(at DESC, id DESC)` with
-- `org_id` optional. Every existing audit index is prefixed by `org_id`
-- (000002) or by `action` (000009), so the unfiltered page was a sequential scan
-- plus a sort of the whole table — the one query that grows without bound as
-- the platform does.
CREATE INDEX audit_log_at_idx ON audit_log (at DESC, id DESC);

-- The renter directory searches `users.full_name` / `users.phone` and
-- `renter_profiles.full_name` with ILIKE '%q%', which no b-tree can serve. The
-- rows it scans are bounded by the org's own contracts (the query joins through
-- them), so the scan is per-org and small; a trigram index is deferred until an
-- org's directory is large enough to need one.
