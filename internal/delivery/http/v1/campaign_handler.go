package v1

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/usecase/campaign"
)

type CampaignHandler struct {
	uc *campaign.UseCase
}

func NewCampaignHandler(uc *campaign.UseCase) *CampaignHandler {
	return &CampaignHandler{uc: uc}
}

// Draft godoc
// @Summary      Draft a broadcast campaign from a free-text instruction
// @Description  Gemini translates the instruction into a segment (how long quiet, which channel, exclude buyers or not) and a draft message, then this resolves real recipients and annotates each with whether it's actually sendable right now (Instagram's 24-hour messaging window). Nothing is sent — see POST /v1/campaigns/send.
// @Tags         campaigns
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body DraftCampaignRequest true "Instruction"
// @Success      200 {object} response.Envelope{data=CampaignDraftResponse}
// @Router       /v1/campaigns/draft [post]
func (h *CampaignHandler) Draft(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req DraftCampaignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	draft, err := h.uc.Draft(c.Request.Context(), orgID, req.Instruction)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toCampaignDraftResponse(draft))
}

// Send godoc
// @Summary      Send a campaign message to admin-approved recipients
// @Description  Only ever called after an admin has reviewed (and optionally edited) a draft from POST /v1/campaigns/draft — conversation_ids here IS the approval, there is no server-side draft to reference. Re-checks Instagram's 24-hour window fresh per recipient at send time. A failure on one recipient doesn't stop the rest — see the response's skipped list.
// @Tags         campaigns
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body SendCampaignRequest true "Message and approved recipients"
// @Success      200 {object} response.Envelope{data=CampaignSendResponse}
// @Router       /v1/campaigns/send [post]
func (h *CampaignHandler) Send(c *gin.Context) {
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

	var req SendCampaignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	conversationIDs := make([]uuid.UUID, 0, len(req.ConversationIDs))
	for _, raw := range req.ConversationIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.Error(apperror.InvalidInput("invalid conversation_id: "+raw, err))
			return
		}
		conversationIDs = append(conversationIDs, id)
	}

	result, err := h.uc.Send(c.Request.Context(), orgID, userID, req.Message, conversationIDs)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toCampaignSendResponse(result))
}

func toCampaignDraftResponse(d *campaign.CampaignDraft) CampaignDraftResponse {
	recipients := make([]CampaignRecipientResponse, 0, len(d.Recipients))
	for _, r := range d.Recipients {
		recipients = append(recipients, CampaignRecipientResponse{
			ConversationID:        r.ConversationID.String(),
			CustomerUsername:      r.CustomerUsername,
			Channel:               string(r.Channel),
			LastCustomerMessageAt: r.LastCustomerMessageAt.Format(time.RFC3339),
			Eligible:              r.Eligible,
			IneligibleReason:      r.IneligibleReason,
		})
	}
	return CampaignDraftResponse{
		Message:                 d.Message,
		MinDaysSinceLastMessage: d.MinDaysSinceLastMessage,
		MaxDaysSinceLastMessage: d.MaxDaysSinceLastMessage,
		Channel:                 d.Channel,
		ExcludeCustomersWhoPaid: d.ExcludeCustomersWhoPaid,
		Recipients:              recipients,
		EligibleCount:           d.EligibleCount,
		IneligibleCount:         d.IneligibleCount,
	}
}

func toCampaignSendResponse(r *campaign.CampaignResult) CampaignSendResponse {
	skipped := make([]CampaignSkippedResponse, 0, len(r.Skipped))
	for _, s := range r.Skipped {
		skipped = append(skipped, CampaignSkippedResponse{
			ConversationID: s.ConversationID.String(),
			Reason:         s.Reason,
		})
	}
	return CampaignSendResponse{
		SentCount: r.SentCount,
		Skipped:   skipped,
	}
}
