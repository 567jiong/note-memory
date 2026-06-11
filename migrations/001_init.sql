-- Reading Memory Agent - Initial Schema

CREATE TABLE IF NOT EXISTS novels (
    id BIGSERIAL PRIMARY KEY,
    title VARCHAR(500) NOT NULL,
    author VARCHAR(200) DEFAULT '',
    total_chapters INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chapters (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT REFERENCES novels(id) ON DELETE CASCADE,
    chapter_number INT NOT NULL,
    title VARCHAR(500) DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    summary TEXT DEFAULT '',
    characters JSONB DEFAULT '[]',
    events JSONB DEFAULT '[]',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(novel_id, chapter_number)
);

CREATE INDEX IF NOT EXISTS idx_chapters_novel_number ON chapters(novel_id, chapter_number);

CREATE TABLE IF NOT EXISTS reading_progress (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT REFERENCES novels(id) ON DELETE CASCADE,
    current_chapter INT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(novel_id)
);

-- Recap cache
CREATE TABLE IF NOT EXISTS recaps (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT REFERENCES novels(id) ON DELETE CASCADE,
    current_chapter INT NOT NULL,
    recap_content TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(novel_id, current_chapter)
);
