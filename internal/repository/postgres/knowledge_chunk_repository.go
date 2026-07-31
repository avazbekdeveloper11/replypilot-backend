// Package postgres (this file): knowledge_base_chunks has a pgvector
// `embedding vector(768)` column (see migration 000005). This codebase
// has no pgvector Go driver dependency (github.com/pgvector/pgvector-go
// or similar) — deliberately, since there's no Go toolchain available
// anywhere in this project's development environment to verify a new
// dependency actually resolves and compiles (see backend/README.md's
// "known limitations"). Instead, embeddings are formatted as pgvector's
// own text literal ("[0.1,0.2,...]") and passed as a plain string
// parameter with a `::vector` cast in raw SQL — the standard fallback
// pgvector documents for drivers without native vector support.
package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type KnowledgeChunkRepository struct {
	db *gorm.DB
}

func NewKnowledgeChunkRepository(db *gorm.DB) *KnowledgeChunkRepository {
	return &KnowledgeChunkRepository{db: db}
}

func formatVector(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (r *KnowledgeChunkRepository) CreateBatch(ctx context.Context, chunks []*entity.KnowledgeChunk, embeddings [][]float32) error {
	if len(chunks) != len(embeddings) {
		return apperror.Internal("create knowledge chunks", fmt.Errorf("chunks (%d) and embeddings (%d) length mismatch", len(chunks), len(embeddings)))
	}
	if len(chunks) == 0 {
		return nil
	}

	orgID := chunks[0].OrganizationID
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		for i, chunk := range chunks {
			id := chunk.ID
			if id == uuid.Nil {
				id = uuid.New()
			}
			res := tx.Exec(
				`INSERT INTO knowledge_base_chunks
					(id, organization_id, document_id, chunk_index, content, token_count, embedding)
				VALUES (?, ?, ?, ?, ?, ?, ?::vector)`,
				id, chunk.OrganizationID, chunk.DocumentID, chunk.ChunkIndex, chunk.Content, chunk.TokenCount,
				formatVector(embeddings[i]),
			)
			if res.Error != nil {
				return res.Error
			}
			chunk.ID = id
		}
		return nil
	})
	if err != nil {
		return apperror.Internal("create knowledge chunks", err)
	}
	return nil
}

func (r *KnowledgeChunkRepository) DeleteByDocument(ctx context.Context, orgID, documentID uuid.UUID) error {
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ? AND document_id = ?", orgID, documentID).
			Delete(&KnowledgeChunkModel{}).Error
	})
	if err != nil {
		return apperror.Internal("delete knowledge chunks by document", err)
	}
	return nil
}

// Search returns the `limit` chunks in this org whose embedding is
// closest (by cosine distance, via pgvector's `<=>` operator and the HNSW
// index on that column) to queryEmbedding. Similarity is reported as
// 1 - distance (pgvector's cosine operator returns distance, 0 = identical).
func (r *KnowledgeChunkRepository) Search(ctx context.Context, orgID uuid.UUID, queryEmbedding []float32, limit int) ([]repository.ChunkSearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	type searchRow struct {
		ID         uuid.UUID
		DocumentID uuid.UUID
		ChunkIndex int
		Content    string
		TokenCount *int
		Distance   float64
	}
	var results []searchRow

	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Raw(
			`SELECT id, document_id, chunk_index, content, token_count,
					(embedding <=> ?::vector) AS distance
				FROM knowledge_base_chunks
				WHERE organization_id = ?
				ORDER BY embedding <=> ?::vector
				LIMIT ?`,
			formatVector(queryEmbedding), orgID, formatVector(queryEmbedding), limit,
		).Scan(&results).Error
	})
	if err != nil {
		return nil, apperror.Internal("search knowledge chunks", err)
	}

	out := make([]repository.ChunkSearchResult, 0, len(results))
	for _, row := range results {
		out = append(out, repository.ChunkSearchResult{
			Chunk: &entity.KnowledgeChunk{
				ID:             row.ID,
				OrganizationID: orgID,
				DocumentID:     row.DocumentID,
				ChunkIndex:     row.ChunkIndex,
				Content:        row.Content,
				TokenCount:     row.TokenCount,
			},
			Similarity: 1 - row.Distance,
		})
	}
	return out, nil
}
