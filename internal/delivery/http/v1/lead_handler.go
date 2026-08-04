package v1

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/lead"
)

type LeadHandler struct {
	uc *lead.UseCase
}

func NewLeadHandler(uc *lead.UseCase) *LeadHandler {
	return &LeadHandler{uc: uc}
}

// List godoc
// @Summary      List leads (customers who left a phone number)
// @Description  Defaults to every status. Pass status=new to see only leads nobody has followed up on yet — that's what the Leads page's default view uses.
// @Tags         leads
// @Produce      json
// @Security     BearerAuth
// @Param        status query string false "filter by status: new, contacted, done"
// @Success      200 {object} response.Envelope{data=[]LeadResponse}
// @Router       /v1/leads [get]
func (h *LeadHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var status *entity.LeadStatus
	if statusParam := c.Query("status"); statusParam != "" {
		s := entity.LeadStatus(statusParam)
		status = &s
	}

	leads, err := h.uc.List(c.Request.Context(), orgID, status)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]LeadResponse, 0, len(leads))
	for _, l := range leads {
		out = append(out, toLeadResponse(l))
	}
	response.OK(c, out)
}

// UpdateStatus godoc
// @Summary      Update a lead's follow-up status
// @Tags         leads
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id      path string                   true "Lead ID"
// @Param        request body UpdateLeadStatusRequest   true "New status"
// @Success      200 {object} response.Envelope{data=LeadResponse}
// @Router       /v1/leads/{id} [patch]
func (h *LeadHandler) UpdateStatus(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid lead id", err))
		return
	}

	var req UpdateLeadStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	l, err := h.uc.UpdateStatus(c.Request.Context(), orgID, id, entity.LeadStatus(req.Status))
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toLeadResponse(l))
}

func toLeadResponse(l *entity.Lead) LeadResponse {
	return LeadResponse{
		ID:               l.ID.String(),
		ConversationID:   l.ConversationID.String(),
		CustomerUsername: l.CustomerUsername,
		Phone:            l.Phone,
		Summary:          l.Summary,
		Status:           string(l.Status),
		CreatedAt:        l.CreatedAt.Format(time.RFC3339),
	}
}
