package postgres

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type AIResponseRepository struct {
	db *gorm.DB
}

func NewAIResponseRepository(db *gorm.DB) *AIResponseRepository {
	return &AIResponseRepository{db: db}
}

func (r *AIResponseRepository) Create(ctx context.Context, resp *entity.AIResponse, citations []*entity.AIResponseCitation) error {
	model := &AIResponseModel{
		ID:                  resp.ID,
		OrganizationID:      resp.OrganizationID,
		ConversationID:      resp.ConversationID,
		MessageID:           resp.MessageID,
		MessageCreatedAt:    resp.MessageCreatedAt,
		ModelUsed:           resp.ModelUsed,
		PromptTokens:        resp.PromptTokens,
		CompletionTokens:    resp.CompletionTokens,
		ConfidenceScore:     resp.ConfidenceScore,
		WasHandoffTriggered: resp.WasHandoffTriggered,
		LatencyMs:           resp.LatencyMs,
	}
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, resp.OrganizationID, func(tx *gorm.DB) error {
		if err := tx.Create(model).Error; err != nil {
			return err
		}

		if len(citations) == 0 {
			return nil
		}
		citationModels := make([]*AIResponseCitationModel, 0, len(citations))
		for _, c := range citations {
			citationModels = append(citationModels, &AIResponseCitationModel{
				AIResponseID:     model.ID,
				KnowledgeChunkID: c.KnowledgeChunkID,
				OrganizationID:   resp.OrganizationID,
				SimilarityScore:  c.SimilarityScore,
			})
		}
		return tx.Create(&citationModels).Error
	})
	if err != nil {
		return apperror.Internal("create ai response", err)
	}

	resp.ID = model.ID
	resp.CreatedAt = model.CreatedAt
	return nil
}
