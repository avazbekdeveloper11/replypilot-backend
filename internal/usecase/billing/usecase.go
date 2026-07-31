// Package billing wraps Stripe Checkout (subscribe) and the Stripe Billing
// Portal (manage/cancel/update payment method) around this codebase's
// plans/subscriptions tables, plus the webhook handler that keeps the
// local `subscriptions` row in sync with what Stripe reports.
//
// SCOPE — READ BEFORE ASSUMING THIS IS A FULL BILLING SYSTEM
//
//	This is self-serve subscribe + Stripe-hosted management, not a custom
//	in-app billing UI. Payment method entry, invoice history, and
//	cancellation all happen on Stripe's own hosted pages (Checkout, Billing
//	Portal) — this codebase never touches a card number (PCI scope) and
//	does not maintain its own `invoices` UI, even though the `invoices`
//	table exists in the schema (see database/schema.sql §13). Building a
//	redundant in-app invoice list/download flow when Stripe's Portal
//	already provides one was a deliberate scope cut, not an oversight.
//
//	There is also no auto-created trial subscription at organization
//	creation — an org has NO Subscription row until it completes Checkout
//	at least once. GetCurrentSubscription returns apperror.NotFound for
//	such an org; the frontend's Billing page is expected to treat that as
//	"no active plan yet, show the upgrade options" rather than an error
//	state.
package billing

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/integration/stripeapi"
)

// BillingPeriod selects which of a Plan's two Stripe prices (monthly or
// yearly) a checkout session is created against.
type BillingPeriod string

const (
	BillingPeriodMonthly BillingPeriod = "monthly"
	BillingPeriodYearly  BillingPeriod = "yearly"
)

// StripeClient is the narrow port this usecase needs onto Stripe —
// satisfied by internal/integration/stripeapi.Client. Uses
// stripeapi.CheckoutSessionParams directly (not a locally-redeclared
// shape) — same precedent as internal/usecase/ai.Generator referencing
// geminiapi.GenerateUsage: a plain data-carrier struct with no
// vendor-specific behavior costs nothing to depend on directly, unlike
// depending on a vendor's client/SDK type.
type StripeClient interface {
	CreateCheckoutSession(ctx context.Context, p stripeapi.CheckoutSessionParams) (string, error)
	CreatePortalSession(ctx context.Context, stripeCustomerID, returnURL string) (string, error)
}

type UseCase struct {
	planRepo      repository.PlanRepository
	subRepo       repository.SubscriptionRepository
	userRepo      repository.UserRepository
	stripe        StripeClient
	webAppURL     string // base URL for Checkout/Portal success/cancel/return redirects
	webhookSecret string // STRIPE_WEBHOOK_SECRET — see HandleWebhookEvent in webhook.go
}

func New(
	planRepo repository.PlanRepository,
	subRepo repository.SubscriptionRepository,
	userRepo repository.UserRepository,
	stripe StripeClient,
	webAppURL string,
	webhookSecret string,
) *UseCase {
	return &UseCase{
		planRepo:      planRepo,
		subRepo:       subRepo,
		userRepo:      userRepo,
		stripe:        stripe,
		webAppURL:     webAppURL,
		webhookSecret: webhookSecret,
	}
}

func (uc *UseCase) ListPlans(ctx context.Context) ([]*entity.Plan, error) {
	return uc.planRepo.ListActive(ctx)
}

func (uc *UseCase) GetCurrentSubscription(ctx context.Context, orgID uuid.UUID) (*entity.Subscription, *entity.Plan, error) {
	sub, err := uc.subRepo.FindActiveByOrganization(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	plan, err := uc.planRepo.FindByID(ctx, sub.PlanID)
	if err != nil {
		return nil, nil, err
	}
	return sub, plan, nil
}

// CreateCheckoutSession resolves planCode -> the Stripe price for the
// requested period, and userID -> an email Stripe pre-fills into Checkout,
// then returns the hosted Checkout URL to redirect the browser to.
func (uc *UseCase) CreateCheckoutSession(ctx context.Context, orgID, userID uuid.UUID, planCode string, period BillingPeriod) (string, error) {
	plan, err := uc.planRepo.FindByCode(ctx, planCode)
	if err != nil {
		return "", err
	}

	var priceID *string
	switch period {
	case BillingPeriodYearly:
		priceID = plan.StripePriceIDYearly
	case BillingPeriodMonthly, "":
		priceID = plan.StripePriceIDMonthly
	default:
		return "", apperror.InvalidInput("period must be 'monthly' or 'yearly'", nil)
	}
	if priceID == nil || *priceID == "" {
		// Expected for 'enterprise' (custom pricing, no self-serve price —
		// see migrations/000006's doc comment) and for any plan whose Stripe
		// price hasn't been configured for this environment yet.
		return "", apperror.InvalidInput(fmt.Sprintf("plan %q has no self-serve price for the %s period — contact sales", planCode, period), nil)
	}

	user, err := uc.userRepo.FindByID(ctx, userID)
	if err != nil {
		return "", err
	}

	return uc.stripe.CreateCheckoutSession(ctx, stripeapi.CheckoutSessionParams{
		PriceID:        *priceID,
		CustomerEmail:  user.Email,
		SuccessURL:     uc.webAppURL + "/billing?checkout=success",
		CancelURL:      uc.webAppURL + "/billing?checkout=cancelled",
		OrganizationID: orgID.String(),
	})
}

// CreatePortalSession requires an existing Stripe customer — i.e. the org
// has completed Checkout at least once. apperror.NotFound (bubbled up from
// GetCurrentSubscription) otherwise; the frontend should only show the
// "Manage billing" action once a subscription exists.
func (uc *UseCase) CreatePortalSession(ctx context.Context, orgID uuid.UUID) (string, error) {
	sub, err := uc.subRepo.FindActiveByOrganization(ctx, orgID)
	if err != nil {
		return "", err
	}
	if sub.StripeCustomerID == nil {
		return "", apperror.Internal("subscription has no stripe customer id", nil)
	}
	return uc.stripe.CreatePortalSession(ctx, *sub.StripeCustomerID, uc.webAppURL+"/billing")
}
