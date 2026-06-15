-- Post-migration: fix embedding column dimension to 1024
-- Idempotent: only runs ALTER if the column exists.
-- GORM creates the column via struct tag; this ensures the type dimension is correct.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'chapters' AND column_name = 'embedding'
    ) THEN
        ALTER TABLE chapters ALTER COLUMN embedding TYPE vector(1024);
    END IF;
END $$;
