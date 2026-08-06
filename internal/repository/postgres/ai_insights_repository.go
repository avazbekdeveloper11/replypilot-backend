package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type AIInsightsRepository struct {
	db *gorm.DB
}

func NewAIInsightsRepository(db *gorm.DB) *AIInsightsRepository {
	return &AIInsightsRepository{db: db}
}

func (r *AIInsightsRepository) Get(ctx context.Context, orgID uuid.UUID) (*entity.AIInsights, error) {
	var model AIInsightsCacheModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("ai insights not generated yet")
		}
		return nil, apperror.Internal("find ai insights", err)
	}
	return modelToAIInsights(&model), nil
}

// Upsert overwrites the org's single cached row in place — see
// entity.AIInsights' doc comment on why there's no history kept. Plain
// INSERT ... ON CONFLICT, same style as PlatformSettingsRepository.Set: a
// one-row-per-key table with no other write path doesn't need GORM's
// clause.OnConflict builder.
func (r *AIInsightsRepository) Upsert(ctx context.Context, insights *entity.AIInsights) error {
	err := withTenant(ctx, r.db, insights.OrganizationID, func(tx *gorm.DB) error {
		return tx.Exec(
			`INSERT INTO ai_insights_cache (organization_id, summary, sales_count, sales_amount_cents, lead_count, conversation_count, generated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (organization_id) DO UPDATE SET
			   summary = EXCLUDED.summary,
			   sales_count = EXCLUDED.sales_count,
			   sales_amount_cents = EXCLUDED.sales_amount_cents,
			   lead_count = EXCLUDED.lead_count,
			   conversation_count = EXCLUDED.conversation_count,
			   generated_at = EXCLUDED.generated_at`,
			insights.OrganizationID, insights.Summary, insights.SalesCount, insights.SalesAmountCents,
			insights.LeadCount, insights.ConversationCount, insights.GeneratedAt,
		).Error
	})
	if err != nil {
		return apperror.Internal("upsert ai insights", err)
	}
	return nil
}

func modelToAIInsights(m *AIInsightsCacheModel) *entity.AIInsights {
	return &entity.AIInsights{
		OrganizationID:    m.OrganizationID,
		Summary:           m.Summary,
		SalesCount:        m.SalesCount,
		SalesAmountCents:  m.SalesAmountCents,
		LeadCount:         m.LeadCount,
		ConversationCount: m.ConversationCount,
		GeneratedAt:       m.GeneratedAt,
	}
}
