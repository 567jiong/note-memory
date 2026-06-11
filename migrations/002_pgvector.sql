-- Phase 2: pgvector RAG 支持

-- 1. 启用 pgvector 扩展
CREATE EXTENSION IF NOT EXISTS vector;

-- 2. chapters 表加 embedding 列（BAAI/bge-large-zh-v1.5: 1024 维）
ALTER TABLE chapters ADD COLUMN IF NOT EXISTS embedding vector(1024);

-- 3. 向量索引（IVFFlat，适合百万级以下数据）
--    注意：表需要有足够数据后才能建索引，这里先预留
-- CREATE INDEX IF NOT EXISTS idx_chapters_embedding ON chapters USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- 4. Q&A 缓存表
CREATE TABLE IF NOT EXISTS qa_cache (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT REFERENCES novels(id) ON DELETE CASCADE,
    current_chapter INT NOT NULL,
    question TEXT NOT NULL,
    answer TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_qa_novel_chapter ON qa_cache(novel_id, current_chapter);
