// Package analytics is thin on purpose — every method is a direct
// pass-through to repository.AnalyticsRepository's aggregate queries, same
// shape as internal/usecase/dashboard. No business logic lives here
// because there isn't any: analytics IS the query.
package analytics

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/repository"
)

type UseCase struct {
	repo repository.AnalyticsRepository
}

func New(repo repository.AnalyticsRepository) *UseCase {
	return &UseCase{repo: repo}
}

func (uc *UseCase) ResponseTimePerDay(ctx context.Context, orgID uuid.UUID, days int) ([]repository.ResponseTimePerDay, error) {
	return uc.repo.ResponseTimePerDay(ctx, orgID, days)
}

func (uc *UseCase) AIUsagePerDay(ctx context.Context, orgID uuid.UUID, days int) ([]repository.AIUsagePerDay, error) {
	return uc.repo.AIUsagePerDay(ctx, orgID, days)
}

func (uc *UseCase) ConversationOutcomes(ctx context.Context, orgID uuid.UUID) (*repository.ConversationOutcomes, error) {
	return uc.repo.ConversationOutcomes(ctx, orgID)
}
