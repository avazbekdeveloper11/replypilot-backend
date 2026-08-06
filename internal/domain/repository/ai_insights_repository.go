package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type AIInsightsRepository interface {
	// Get returns apperror.NotFound when the org has never generated
	// insights — internal/usecase/insights.UseCase.Get translates that into
	// (nil, nil), same convention as click.UseCase.Get, so the frontend can
	// treat "never generated" as "show a generate button" without an
	// error-type switch.
	Get(ctx context.Context, orgID uuid.UUID) (*entity.AIInsights, error)
	Upsert(ctx context.Context, insights *entity.AIInsights) error
}
