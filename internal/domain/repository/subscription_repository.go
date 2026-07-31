package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type SubscriptionRepository interface {
	// FindActiveByOrganization returns the org's current "live" subscription
	// (trialing/active/past_due/paused — matches uq_subscriptions_org_active
	// in migrations/000001, which is the DB-level backstop against ever
	// having two). apperror.NotFound if the org has never completed
	// Checkout.
	FindActiveByOrganization(ctx context.Context, orgID uuid.UUID) (*entity.Subscription, error)
	// FindByStripeSubscriptionID backs webhook upserts — Stripe is the
	// source of truth for which local row a customer.subscription.* event
	// corresponds to.
	FindByStripeSubscriptionID(ctx context.Context, stripeSubscriptionID string) (*entity.Subscription, error)
	Create(ctx context.Context, sub *entity.Subscription) error
	Update(ctx context.Context, sub *entity.Subscription) error
}
