-- name: DBHealthCheck :one
-- Trivial round-trip used by the health endpoint and to keep `sqlc generate`
-- meaningful before Phase 1 lands the real schema.
SELECT 1 AS ok;
