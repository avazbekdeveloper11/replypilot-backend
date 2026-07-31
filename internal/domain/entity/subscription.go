package entity

import (
	"time"

	"github.com/google/uuid"
)

type SubscriptionStatus string

const (
	SubscriptionStatusTrialing SubscriptionStatus = "trialing"
	SubscriptionStatusActive   SubscriptionStatus = "active"
	SubscriptionStatusPastDue  SubscriptionStatus = "past_due"
	SubscriptionStatusCanceled SubscriptionStatus = "canceled"
	SubscriptionStatusUnpaid   SubscriptionStatus = "unpaid"
	SubscriptionStatusPaused   SubscriptionStatus = "paused"
)

// Subscription is one organization's relationship to a Plan, mirrored from
// Stripe — StripeSubscriptionID/StripeCustomerID are nil until the org has
// actually completed Stripe Checkout at least once (see
// usecase/billing.UseCase.HandleWebhook). An org with no Subscription row
// yet is simply unpaid/on no plan; this codebase does not auto-create a
// trial subscription at org creation time (an honest scope cut — see that
// usecase's doc comment).
type Subscription struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
	PlanID              uuid.UUID
	StripeSubscriptionID *string
	StripeCustomerID    *string
	Status              SubscriptionStatus
	CurrentPeriodStart  *time.Time
	CurrentPeriodEnd    *time.Time
	CancelAtPeriodEnd   bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}
