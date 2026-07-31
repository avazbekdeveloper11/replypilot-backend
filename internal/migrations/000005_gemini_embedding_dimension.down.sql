-- Migration 000005 — revert knowledge_base_chunks.embedding: 768 -> 1536 (DOWN)

DROP INDEX IF EXISTS idx_kb_chunks_embedding_hnsw;
ALTER TABLE knowledge_base_chunks DROP COLUMN embedding;
ALTER TABLE knowledge_base_chunks ADD COLUMN embedding vector(1536);
CREATE INDEX idx_kb_chunks_embedding_hnsw ON knowledge_base_chunks
    USING hnsw (embedding vector_cosine_ops);
