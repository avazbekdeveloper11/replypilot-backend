package billing

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/integration/stripeapi"
)

// stripeEvent is the envelope every Stripe webhook delivery shares —
// {id, type, data: {object: <the actual resource, shape depends on type>}}.
type stripeEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// stripeSubscriptionObject is the subset of Stripe's Subscription object
// this usecase reads. metadata.organization_id is present because
// stripeapi.CreateCheckoutSession sets subscription_data[metadata] at
// Checkout time — every subscription this app creates carries it from
// birth, so customer.subscription.created/updated never needs a separate
// lookup back to checkout.session.completed to learn which org it's for.
type stripeSubscriptionObject struct {
	ID       string `json:"id"`
	Customer string `json:"customer"`
	Status   string `json:"status"`
	Metadata struct {
		OrganizationID string `json:"organization_id"`
	} `json:"metadata"`
	CurrentPeriodStart int64 `json:"current_period_start"`
	CurrentPeriodEnd   int64 `json:"current_period_end"`
	CancelAtPeriodEnd  bool  `json:"cancel_at_period_end"`
	Items              struct {
		Data []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

// HandleWebhookEvent verifies the Stripe-Signature header against rawBody
// (stripeapi.VerifyWebhookSignature — same division of responsibility as
// instagram.WebhookUseCase.Process's HMAC check: verification and
// processing live together in the usecase, not split across the handler
// and usecase) and, on success, processes the event.
//
// Only customer.subscription.{created,updated,deleted} are handled.
// checkout.session.completed is deliberately NOT handled: this app sets
// subscription_data.metadata at Checkout creation time (see
// stripeapi.CreateCheckoutSession), so the subscription object itself
// already carries organization_id by the time customer.subscription.created
// fires — there's no information checkout.session.completed would add.
// Every other event type is a documented, silent no-op (returns nil) —
// this codebase has no invoice/payment-history UI to feed (see this
// package's doc comment), so invoice.* events aren't consumed either.
func (uc *UseCase) HandleWebhookEvent(ctx context.Context, rawBody []byte, sigHeader string) error {
	if uc.webhookSecret == "" {
		// No STRIPE_WEBHOOK_SECRET configured — refuse rather than process
		// an unverifiable payload. See config.StripeConfig's doc comment on
		// why this isn't mustGetEnv (the rest of the API still boots), but
		// this endpoint specifically can't safely skip signature
		// verification.
		return apperror.Internal("stripe webhook secret not configured", nil)
	}
	if err := stripeapi.VerifyWebhookSignature(rawBody, sigHeader, uc.webhookSecret); err != nil {
		return apperror.Unauthorized("invalid stripe webhook signature")
	}

	var evt stripeEvent
	if err := json.Unmarshal(rawBody, &evt); err != nil {
		return apperror.InvalidInput("malformed stripe webhook payload", err)
	}

	switch evt.Type {
	case "customer.subscription.created", "customer.subscription.updated":
		return uc.upsertSubscriptionFromStripe(ctx, evt.Data.Object)
	case "customer.subscription.deleted":
		return uc.markSubscriptionCanceled(ctx, evt.Data.Object)
	default:
		return nil
	}
}

func (uc *UseCase) upsertSubscriptionFromStripe(ctx context.Context, raw json.RawMessage) error {
	var obj stripeSubscriptionObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return apperror.InvalidInput("malformed stripe subscription object", err)
	}
	if len(obj.Items.Data) == 0 {
		return apperror.InvalidInput("stripe subscription has no line items", nil)
	}

	plan, err := uc.planRepo.FindByStripePriceID(ctx, obj.Items.Data[0].Price.ID)
	if err != nil {
		return err
	}

	periodStart := time.Unix(obj.CurrentPeriodStart, 0)
	periodEnd := time.Unix(obj.CurrentPeriodEnd, 0)
	customerID := obj.Customer
	subscriptionID := obj.ID

	existing, err := uc.subRepo.FindByStripeSubscriptionID(ctx, obj.ID)
	if err == nil {
		existing.PlanID = plan.ID
		existing.Status = entity.SubscriptionStatus(obj.Status)
		existing.CurrentPeriodStart = &periodStart
		existing.CurrentPeriodEnd = &periodEnd
		existing.CancelAtPeriodEnd = obj.CancelAtPeriodEnd
		existing.StripeCustomerID = &customerID
		return uc.subRepo.Update(ctx, existing)
	}
	if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
		return err
	}

	// First time seeing this Stripe subscription — this is the
	// customer.subscription.created path. organization_id has to come from
	// the subscription's own metadata (see this file's doc comment); a
	// missing/malformed value here means this subscription wasn't created
	// through this app's CreateCheckoutSession, which should never happen
	// in practice but is treated as an error rather than silently dropped.
	orgID, parseErr := uuid.Parse(obj.Metadata.OrganizationID)
	if parseErr != nil {
		return apperror.InvalidInput("stripe subscription metadata missing a valid organization_id", parseErr)
	}

	sub := &entity.Subscription{
		OrganizationID:       orgID,
		PlanID:               plan.ID,
		StripeSubscriptionID: &subscriptionID,
		StripeCustomerID:     &customerID,
		Status:               entity.SubscriptionStatus(obj.Status),
		CurrentPeriodStart:   &periodStart,
		CurrentPeriodEnd:     &periodEnd,
		CancelAtPeriodEnd:    obj.CancelAtPeriodEnd,
	}
	return uc.subRepo.Create(ctx, sub)
}

func (uc *UseCase) markSubscriptionCanceled(ctx context.Context, raw json.RawMessage) error {
	var obj stripeSubscriptionObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return apperror.InvalidInput("malformed stripe subscription object", err)
	}

	sub, err := uc.subRepo.FindByStripeSubscriptionID(ctx, obj.ID)
	if err != nil {
		// A deleted-event for a subscription we never recorded isn't
		// actionable — nothing to mark canceled. Not treated as an error;
		// Stripe doesn't need (or want) a retry for this.
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil
		}
		return err
	}

	sub.Status = entity.SubscriptionStatusCanceled
	return uc.subRepo.Update(ctx, sub)
}
