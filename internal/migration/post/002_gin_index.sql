-- Post-migration: GIN index for full-text search
-- GORM does not support USING GIN, so this must live as raw SQL.

CREATE INDEX IF NOT EXISTS idx_chapters_tsv ON chapters USING GIN (tsv);
