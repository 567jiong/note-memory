-- Post-migration: full-text search columns on chapters
-- These columns are NOT managed by GORM struct tags and must live as raw SQL.

ALTER TABLE chapters ADD COLUMN IF NOT EXISTS search_text TEXT DEFAULT '';
ALTER TABLE chapters ADD COLUMN IF NOT EXISTS tsv tsvector;
