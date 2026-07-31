package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type KnowledgeDocumentRepository interface {
	Create(ctx context.Context, doc *entity.KnowledgeDocument) error
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.KnowledgeDocument, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.KnowledgeDocument, error)
	Update(ctx context.Context, doc *entity.KnowledgeDocument) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

// ChunkSearchResult is one hit from KnowledgeChunkRepository.Search —
// Similarity is cosine similarity (1 - cosine_distance, since pgvector's
// <=> operator returns distance, not similarity), 0-1, higher is closer.
type ChunkSearchResult struct {
	Chunk      *entity.KnowledgeChunk
	Similarity float64
}

type KnowledgeChunkRepository interface {
	// CreateBatch writes every chunk of one document plus its embedding in
	// a single transaction — a document is either fully ingested or not
	// ingested at all, never half.
	CreateBatch(ctx context.Context, chunks []*entity.KnowledgeChunk, embeddings [][]float32) error
	DeleteByDocument(ctx context.Context, orgID, documentID uuid.UUID) error
	// Search is cosine-similarity nearest-neighbor over the org's chunks —
	// the retrieval half of RAG. Used by internal/usecase/ai (task: AI
	// reply pipeline), not yet called by anything in this milestone.
	Search(ctx context.Context, orgID uuid.UUID, queryEmbedding []float32, limit int) ([]ChunkSearchResult, error)
}
