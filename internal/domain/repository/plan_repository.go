package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type PlanRepository interface {
	ListActive(ctx context.Context) ([]*entity.Plan, error)
	FindByCode(ctx context.Context, code string) (*entity.Plan, error)
	FindByID(ctx context.Context, id uuid.UUID) (*entity.Plan, error)
	// FindByStripePriceID matches either StripePriceIDMonthly or
	// StripePriceIDYearly — the Stripe webhook only knows which price a
	// subscription is on, not which of the two cadences it maps to
	// locally, so this checks both. Backs the webhook's subscription ->
	// local plan resolution.
	FindByStripePriceID(ctx context.Context, stripePriceID string) (*entity.Plan, error)
}
