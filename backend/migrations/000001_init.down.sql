-- Extensions are left in place on rollback: dropping pgcrypto would break any
-- other object depending on it, and re-creating it is idempotent.
-- Intentional no-op.
SELECT 1;
