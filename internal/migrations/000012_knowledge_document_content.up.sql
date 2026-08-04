-- Stores the raw source text a knowledge base document was ingested from
-- (pasted text, or a .txt/.md file's decoded contents) so it can be edited
-- later without deleting and re-uploading. The chunks in
-- knowledge_base_chunks remain the retrieval index the AI actually
-- searches; this column is the editable source of truth they're derived
-- from — see entity.KnowledgeDocument.Content's doc comment.
ALTER TABLE knowledge_base_documents ADD COLUMN content text;
