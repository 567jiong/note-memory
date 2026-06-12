-- 004_chunks.sql: Chapter content chunking for fine-grained embedding search
-- Each chapter is split into overlapping chunks at sentence boundaries.
-- Chunk embeddings capture specific details that chapter-level summary embeddings may miss.

CREATE TABLE IF NOT EXISTS chapter_chunks (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    chapter_id BIGINT NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
    chunk_index INT NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    embedding vector(1024),
    char_start INT NOT NULL DEFAULT 0,
    char_end INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_chunks_chapter ON chapter_chunks(chapter_id);
CREATE INDEX IF NOT EXISTS idx_chunks_novel ON chapter_chunks(novel_id);

-- Composite index for the most common query pattern:
-- "find top-K chunks for a novel, within spoiler-free chapter range, ordered by similarity"
-- Note: IVFFlat index on embedding should be created later when data volume is sufficient.
CREATE INDEX IF NOT EXISTS idx_chunks_novel_chapter ON chapter_chunks(novel_id, chapter_id);
