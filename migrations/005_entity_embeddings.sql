-- 005_entity_embeddings: 实体向量表，用于语义匹配人物别名/马甲/称号
-- 每个实体存一段富描述文本 + 1024维 Embedding，用户搜任何别名都能通过向量相似度召回

CREATE TABLE IF NOT EXISTS entity_embeddings (
    id BIGSERIAL PRIMARY KEY,
    novel_id BIGINT NOT NULL REFERENCES novels(id) ON DELETE CASCADE,
    entity_name VARCHAR(200) NOT NULL,
    entity_type VARCHAR(50) NOT NULL DEFAULT 'character',
    description TEXT NOT NULL,
    embedding vector(1024),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_entity_emb_novel_name ON entity_embeddings(novel_id, entity_name);
