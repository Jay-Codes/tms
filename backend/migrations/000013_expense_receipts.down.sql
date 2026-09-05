ALTER TABLE expenses
    DROP COLUMN IF EXISTS receipt_content_type,
    DROP COLUMN IF EXISTS receipt_size;
