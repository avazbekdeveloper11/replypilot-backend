package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type SubscriptionRepository struct {
	db *gorm.DB
}

func NewSubscriptionRepository(db *gorm.DB) *SubscriptionRepository {
	return &SubscriptionRepository{db: db}
}

func (r *SubscriptionRepository) FindActiveByOrganization(ctx context.Context, orgID uuid.UUID) (*entity.Subscription, error) {
	var model SubscriptionModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where(
			"organization_id = ? AND status IN ('trialing','active','past_due','paused')",
			orgID,
		).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("no active subscription")
		}
		return nil, apperror.Internal("find active subscription", err)
	}
	return modelToSubscription(&model), nil
}

// FindByStripeSubscriptionID deliberately does NOT go through withTenant —
// same reasoning as InstagramAccountRepository.FindByIGUserID: the Stripe
// webhook handler knows only the Stripe subscription id at this point, not
// which organization it belongs to (that's what this call resolves).
// Migration 000007 adds the permissive, GUC-gated SELECT policy this
// depends on. See that migration's comment for the full rationale.
func (r *SubscriptionRepository) FindByStripeSubscriptionID(ctx context.Context, stripeSubscriptionID string) (*entity.Subscription, error) {
	var model SubscriptionModel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL app.webhook_lookup = 'on'").Error; err != nil {
			return err
		}
		return tx.First(&model, "stripe_subscription_id = ?", stripeSubscriptionID).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("subscription not found")
		}
		return nil, apperror.Internal("find subscription by stripe subscription id", err)
	}
	return modelToSubscription(&model), nil
}

func (r *SubscriptionRepository) Create(ctx context.Context, sub *entity.Subscription) error {
	model := subscriptionToModel(sub)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, sub.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create subscription", err)
	}
	*sub = *modelToSubscription(model)
	return nil
}

func (r *SubscriptionRepository) Update(ctx context.Context, sub *entity.Subscription) error {
	model := subscriptionToModel(sub)
	var rowsAffected int64
	err := withTenant(ctx, r.db, sub.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&SubscriptionModel{}).Where("id = ?", sub.ID).Updates(map[string]any{
			"plan_id":                model.PlanID,
			"stripe_subscription_id": model.StripeSubscriptionID,
			"stripe_customer_id":     model.StripeCustomerID,
			"status":                 model.Status,
			"current_period_start":   model.CurrentPeriodStart,
			"current_period_end":     model.CurrentPeriodEnd,
			"cancel_at_period_end":   model.CancelAtPeriodEnd,
		})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update subscription", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("subscription not found")
	}
	return nil
}

func subscriptionToModel(s *entity.Subscription) *SubscriptionModel {
	return &SubscriptionModel{
		ID:                   s.ID,
		OrganizationID:       s.OrganizationID,
		PlanID:               s.PlanID,
		StripeSubscriptionID: s.StripeSubscriptionID,
		StripeCustomerID:     s.StripeCustomerID,
		Status:               string(s.Status),
		CurrentPeriodStart:   s.CurrentPeriodStart,
		CurrentPeriodEnd:     s.CurrentPeriodEnd,
		CancelAtPeriodEnd:    s.CancelAtPeriodEnd,
	}
}

func modelToSubscription(m *SubscriptionModel) *entity.Subscription {
	return &entity.Subscription{
		ID:                   m.ID,
		OrganizationID:       m.OrganizationID,
		PlanID:               m.PlanID,
		StripeSubscriptionID: m.StripeSubscriptionID,
		StripeCustomerID:     m.StripeCustomerID,
		Status:               entity.SubscriptionStatus(m.Status),
		CurrentPeriodStart:   m.CurrentPeriodStart,
		CurrentPeriodEnd:     m.CurrentPeriodEnd,
		CancelAtPeriodEnd:    m.CancelAtPeriodEnd,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
}
