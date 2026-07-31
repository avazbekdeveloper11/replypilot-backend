package v1

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/billing"
)

type BillingHandler struct {
	uc *billing.UseCase
}

func NewBillingHandler(uc *billing.UseCase) *BillingHandler {
	return &BillingHandler{uc: uc}
}

// ListPlans godoc
// @Summary      List active subscription plans
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]PlanResponse}
// @Router       /v1/billing/plans [get]
func (h *BillingHandler) ListPlans(c *gin.Context) {
	plans, err := h.uc.ListPlans(c.Request.Context())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]PlanResponse, 0, len(plans))
	for _, p := range plans {
		out = append(out, toPlanResponse(p))
	}
	response.OK(c, out)
}

// GetSubscription godoc
// @Summary      Get the current organization's active subscription
// @Description  404 if the organization has never completed Checkout — see usecase/billing's package doc comment.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=SubscriptionResponse}
// @Failure      404 {object} response.Envelope
// @Router       /v1/billing/subscription [get]
func (h *BillingHandler) GetSubscription(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	sub, plan, err := h.uc.GetCurrentSubscription(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toSubscriptionResponse(sub, plan))
}

// CreateCheckoutSession godoc
// @Summary      Create a Stripe Checkout session to subscribe to a plan
// @Tags         billing
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body CreateCheckoutSessionRequest true "Plan to subscribe to"
// @Success      200 {object} response.Envelope{data=CheckoutSessionResponse}
// @Failure      400 {object} response.Envelope
// @Router       /v1/billing/checkout-session [post]
func (h *BillingHandler) CreateCheckoutSession(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	userID, err := userIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req CreateCheckoutSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}
	period := billing.BillingPeriodMonthly
	if req.Period == "yearly" {
		period = billing.BillingPeriodYearly
	}

	url, err := h.uc.CreateCheckoutSession(c.Request.Context(), orgID, userID, req.PlanCode, period)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, CheckoutSessionResponse{URL: url})
}

// CreatePortalSession godoc
// @Summary      Create a Stripe Billing Portal session
// @Description  The Portal is Stripe-hosted: payment method update, invoice history, and cancellation all happen there, not in this app. Requires an existing subscription (404 otherwise).
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=PortalSessionResponse}
// @Failure      404 {object} response.Envelope
// @Router       /v1/billing/portal-session [post]
func (h *BillingHandler) CreatePortalSession(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	url, err := h.uc.CreatePortalSession(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, PortalSessionResponse{URL: url})
}

// Webhook godoc
// @Summary      Receive a Stripe webhook delivery
// @Description  Verifies the Stripe-Signature header against the raw request body before processing anything — see stripeapi.VerifyWebhookSignature.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Success      200 {object} response.Envelope
// @Failure      401 {object} response.Envelope
// @Router       /webhooks/stripe [post]
func (h *BillingHandler) Webhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	if err := h.uc.HandleWebhookEvent(c.Request.Context(), body, c.GetHeader("Stripe-Signature")); err != nil {
		c.Error(err)
		return
	}
	response.OK(c, gin.H{"received": true})
}

func toPlanResponse(p *entity.Plan) PlanResponse {
	selfServe := (p.StripePriceIDMonthly != nil && *p.StripePriceIDMonthly != "") ||
		(p.StripePriceIDYearly != nil && *p.StripePriceIDYearly != "")
	return PlanResponse{
		Code:              p.Code,
		Name:              p.Name,
		PriceMonthlyCents: p.PriceMonthlyCents,
		PriceYearlyCents:  p.PriceYearlyCents,
		MessageLimit:      p.MessageLimit,
		SeatLimit:         p.SeatLimit,
		Features:          p.Features,
		SelfServe:         selfServe,
	}
}

func toSubscriptionResponse(s *entity.Subscription, p *entity.Plan) SubscriptionResponse {
	resp := SubscriptionResponse{
		Status:            string(s.Status),
		PlanCode:          p.Code,
		PlanName:          p.Name,
		CancelAtPeriodEnd: s.CancelAtPeriodEnd,
	}
	if s.CurrentPeriodEnd != nil {
		formatted := s.CurrentPeriodEnd.Format(time.RFC3339)
		resp.CurrentPeriodEnd = &formatted
	}
	return resp
}
