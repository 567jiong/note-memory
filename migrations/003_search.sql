-- Phase 2.5: 混合检索 — tsvector 全文检索 + 别名支持

-- 1. chapters 表加全文检索列
ALTER TABLE chapters ADD COLUMN IF NOT EXISTS search_text TEXT DEFAULT '';
ALTER TABLE chapters ADD COLUMN IF NOT EXISTS tsv tsvector;

-- 2. 全文检索索引（GIN）
CREATE INDEX IF NOT EXISTS idx_chapters_tsv ON chapters USING GIN (tsv);

-- 3. 术语/别名映射表（从 characters JSON 自动构建）
CREATE TABLE IF NOT EXISTS entity_aliases (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT REFERENCES novels(id) ON DELETE CASCADE,
    canonical_name VARCHAR(200) NOT NULL,
    alias VARCHAR(200) NOT NULL,
    UNIQUE(novel_id, alias)
);
CREATE INDEX IF NOT EXISTS idx_entity_aliases_lookup ON entity_aliases(novel_id, alias);

-- 4. 向量索引（数据量足够后建 IVFFlat）
-- CREATE INDEX IF NOT EXISTS idx_chapters_embedding ON chapters USING ivfflat (embedding vector_cosine_ops) WITH (lists = 50);
