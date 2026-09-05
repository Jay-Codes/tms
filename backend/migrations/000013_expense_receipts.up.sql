-- Phase 10: what a receipt is, beside where it is.
--
-- `expenses.receipt_object_key` (migration 000012) says which object in the
-- `receipts` bucket belongs to an expense. It does not say what that object is,
-- and the ledger needs to: an expense row renders a receipt chip ("PDF, 240 KB")
-- in a list of fifty, and asking MinIO to stat fifty objects to draw fifty chips
-- would put an object-storage round trip on the critical path of every page of
-- the ledger.
--
-- Both columns are written by the upload-completion callback from the object
-- MinIO actually received (SPEC §7: a presigned PUT cannot enforce type or
-- size, so both are checked on completion), never from what the client claimed.
-- They are NULL exactly when receipt_object_key is.
ALTER TABLE expenses
    ADD COLUMN receipt_content_type TEXT,
    ADD COLUMN receipt_size         BIGINT;
