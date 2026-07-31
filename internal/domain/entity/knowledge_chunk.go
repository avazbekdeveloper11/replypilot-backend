package entity

import (
	"time"

	"github.com/google/uuid"
)

// KnowledgeChunk is one embedded slice of a KnowledgeDocument's text — the
// unit the AI reply pipeline's RAG retrieval actually searches over. The
// embedding vector itself is NOT carried on this struct (domain layer
// stays free of any pgvector-specific type); it's written and searched via
// raw SQL in the postgres repository — see that package's doc comment for
// why (no pgvector Go driver dependency in this codebase).
type KnowledgeChunk struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	DocumentID     uuid.UUID
	ChunkIndex     int
	Content        string
	TokenCount     *int
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}
