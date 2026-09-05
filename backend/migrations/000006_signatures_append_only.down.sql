-- The template re-wording is not reversed: the old phrase rendered "day day 5",
-- and restoring it would put a known-broken body back in front of renters.

DROP TRIGGER IF EXISTS contract_signatures_no_delete ON contract_signatures;
DROP TRIGGER IF EXISTS contract_signatures_no_update ON contract_signatures;
DROP FUNCTION IF EXISTS contract_signatures_append_only();
