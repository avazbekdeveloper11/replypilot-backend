package v1

import (
	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/commentautomation"
)

type CommentAutomationHandler struct {
	uc *commentautomation.UseCase
}

func NewCommentAutomationHandler(uc *commentautomation.UseCase) *CommentAutomationHandler {
	return &CommentAutomationHandler{uc: uc}
}

// Get godoc
// @Summary      Get the organization's comment-to-DM automation settings
// @Description  Always returns settings — an org that has never configured this gets enabled=false, not a 404. See commentautomation.UseCase.Get's doc comment.
// @Tags         integrations
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=CommentAutomationResponse}
// @Router       /v1/integrations/comment-automation [get]
func (h *CommentAutomationHandler) Get(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	settings, err := h.uc.Get(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toCommentAutomationResponse(settings))
}

// Update godoc
// @Summary      Update comment-to-DM automation settings
// @Description  Turning this on makes the AI send a private reply (DM) to anyone who comments on the account's posts, and take the conversation from there. See migration 000017 for Meta's one-private-reply-per-comment rule.
// @Tags         integrations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body UpdateCommentAutomationRequest true "Settings"
// @Success      200 {object} response.Envelope{data=CommentAutomationResponse}
// @Router       /v1/integrations/comment-automation [put]
func (h *CommentAutomationHandler) Update(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req UpdateCommentAutomationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	settings, err := h.uc.Update(c.Request.Context(), commentautomation.UpdateInput{
		OrganizationID:  orgID,
		Enabled:         req.Enabled,
		PublicReplyText: req.PublicReplyText,
	})
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toCommentAutomationResponse(settings))
}

func toCommentAutomationResponse(s *entity.CommentAutomationSettings) CommentAutomationResponse {
	return CommentAutomationResponse{
		Enabled:         s.Enabled,
		PublicReplyText: s.PublicReplyText,
	}
}
