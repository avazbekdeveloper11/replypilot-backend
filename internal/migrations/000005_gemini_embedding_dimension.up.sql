-- Migration 000005 — knowledge_base_chunks.embedding: 1536 -> 768 (UP)
--
-- The original schema (database/schema.sql, see the comment above
-- knowledge_base_chunks) sized this column for OpenAI's
-- text-embedding-3-small/ada-002 (1536 dimensions). This project's AI
-- provider is Gemini (internal/integration/geminiapi) — Gemini's
-- text-embedding-004 model outputs 768-dimensional vectors, not 1536.
-- pgvector columns are dimension-fixed; changing it means dropping and
-- recreating the column and its HNSW index (which is itself built
-- against the column's dimension), not an in-place ALTER COLUMN TYPE.
--
-- Safe because this project has no knowledge-base data yet — this
-- migration ships alongside the first knowledge-base usecase code, so
-- there is nothing to re-embed. A fresh column is correct here, not a
-- data-loss shortcut being taken against real data.

DROP INDEX IF EXISTS idx_kb_chunks_embedding_hnsw;
ALTER TABLE knowledge_base_chunks DROP COLUMN embedding;
ALTER TABLE knowledge_base_chunks ADD COLUMN embedding vector(768);
CREATE INDEX idx_kb_chunks_embedding_hnsw ON knowledge_base_chunks
    USING hnsw (embedding vector_cosine_ops);
