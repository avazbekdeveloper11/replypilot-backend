package v1

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/customer"
)

type CustomerHandler struct {
	uc *customer.UseCase
}

func NewCustomerHandler(uc *customer.UseCase) *CustomerHandler {
	return &CustomerHandler{uc: uc}
}

// List godoc
// @Summary      List the organization's customer database
// @Description  Every conversation annotated with its paid-order totals, biggest spenders first — see customer.UseCase.List's doc comment. Includes customers with zero orders (they still belong in the list — an admin deciding who needs a nudge needs to see them too).
// @Tags         customers
// @Produce      json
// @Security     BearerAuth
// @Param        search query string false "Filter by customer username (case-insensitive substring)"
// @Param        segment query string false "Filter by RFM segment (new, champion, loyal, at_risk, sleeping, lost)"
// @Success      200 {object} response.Envelope{data=[]CustomerSummaryResponse}
// @Router       /v1/customers [get]
func (h *CustomerHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	search := c.Query("search")
	segment := entity.RFMSegment(c.Query("segment"))
	summaries, err := h.uc.List(c.Request.Context(), orgID, search, segment)
	if err != nil {
		c.Error(err)
		return
	}

	resp := make([]CustomerSummaryResponse, 0, len(summaries))
	for _, s := range summaries {
		resp = append(resp, toCustomerSummaryResponse(s))
	}
	response.OK(c, resp)
}

// Orders godoc
// @Summary      Get one customer's full order history
// @Description  Every order for this conversation, any status — the customer database's drill-down view, "what did this specific person actually buy". See OrderRepository.ListByConversation's doc comment.
// @Tags         customers
// @Produce      json
// @Security     BearerAuth
// @Param        conversation_id path string true "Conversation id"
// @Success      200 {object} response.Envelope{data=[]CustomerOrderResponse}
// @Router       /v1/customers/{conversation_id}/orders [get]
func (h *CustomerHandler) Orders(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	conversationID, err := uuid.Parse(c.Param("conversation_id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid conversation_id", err))
		return
	}

	orders, err := h.uc.Orders(c.Request.Context(), orgID, conversationID)
	if err != nil {
		c.Error(err)
		return
	}

	resp := make([]CustomerOrderResponse, 0, len(orders))
	for _, o := range orders {
		resp = append(resp, toCustomerOrderResponse(o))
	}
	response.OK(c, resp)
}

func toCustomerSummaryResponse(s *entity.CustomerSummary) CustomerSummaryResponse {
	resp := CustomerSummaryResponse{
		ConversationID:   s.ConversationID.String(),
		Channel:          string(s.Channel),
		CustomerUsername: s.CustomerUsername,
		TotalPaidCents:   s.TotalPaidCents,
		PaidOrderCount:   s.PaidOrderCount,
		Segment:          string(s.Segment),
		RecencyScore:     s.RecencyScore,
		FrequencyScore:   s.FrequencyScore,
		MonetaryScore:    s.MonetaryScore,
	}
	if s.LastMessageAt != nil {
		formatted := s.LastMessageAt.Format(time.RFC3339)
		resp.LastMessageAt = &formatted
	}
	if s.LastPaidAt != nil {
		formatted := s.LastPaidAt.Format(time.RFC3339)
		resp.LastPaidAt = &formatted
	}
	return resp
}

func toCustomerOrderResponse(o *entity.Order) CustomerOrderResponse {
	resp := CustomerOrderResponse{
		ID:          o.ID.String(),
		ProductName: o.ProductNameSnapshot,
		AmountCents: o.AmountCents,
		Currency:    o.Currency,
		Status:      string(o.Status),
		CreatedAt:   o.CreatedAt.Format(time.RFC3339),
	}
	if o.PaidAt != nil {
		formatted := o.PaidAt.Format(time.RFC3339)
		resp.PaidAt = &formatted
	}
	return resp
}
