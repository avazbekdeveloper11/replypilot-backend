package v1

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/usecase/conversation"
)

type ConversationHandler struct {
	uc *conversation.UseCase
}

func NewConversationHandler(uc *conversation.UseCase) *ConversationHandler {
	return &ConversationHandler{uc: uc}
}

// List godoc
// @Summary      List conversations for the current organization
// @Description  Cursor-paginated, newest first. Pass `cursor` (RFC3339 timestamp) from the last item of the previous page to fetch the next one — there is no offset pagination, see database/schema.sql for why.
// @Tags         conversations
// @Produce      json
// @Security     BearerAuth
// @Param        status query string false "filter by status: ai_active, pending_human, human_active, resolved, closed"
// @Param        cursor query string false "RFC3339 timestamp cursor"
// @Param        limit  query int    false "page size, default 20"
// @Success      200 {object} response.Envelope{data=[]ConversationResponse}
// @Router       /v1/conversations [get]
func (h *ConversationHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	params := repository.ConversationListParams{OrganizationID: orgID}

	if statusParam := c.Query("status"); statusParam != "" {
		s := entity.ConversationStatus(statusParam)
		params.Status = &s
	}
	if cursorParam := c.Query("cursor"); cursorParam != "" {
		t, err := time.Parse(time.RFC3339, cursorParam)
		if err != nil {
			c.Error(apperror.InvalidInput("cursor must be an RFC3339 timestamp", err))
			return
		}
		params.CursorBefore = &t
	}
	if limitParam := c.Query("limit"); limitParam != "" {
		n, err := strconv.Atoi(limitParam)
		if err != nil {
			c.Error(apperror.InvalidInput("limit must be an integer", err))
			return
		}
		params.Limit = n
	}

	conversations, err := h.uc.List(c.Request.Context(), params)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]ConversationResponse, 0, len(conversations))
	for _, conv := range conversations {
		out = append(out, toConversationResponse(conv))
	}
	response.OK(c, out)
}

// Get godoc
// @Summary      Get a single conversation
// @Tags         conversations
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Conversation ID"
// @Success      200 {object} response.Envelope{data=ConversationResponse}
// @Failure      404 {object} response.Envelope
// @Router       /v1/conversations/{id} [get]
func (h *ConversationHandler) Get(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid conversation id", err))
		return
	}

	conv, err := h.uc.Get(c.Request.Context(), orgID, id)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toConversationResponse(conv))
}

// ListMessages godoc
// @Summary      List messages in a conversation
// @Description  Cursor-paginated, newest first, same convention as GET /v1/conversations.
// @Tags         conversations
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  string true  "Conversation ID"
// @Param        cursor query string false "RFC3339 timestamp cursor"
// @Param        limit  query int    false "page size, default 50"
// @Success      200 {object} response.Envelope{data=[]MessageResponse}
// @Failure      404 {object} response.Envelope
// @Router       /v1/conversations/{id}/messages [get]
func (h *ConversationHandler) ListMessages(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid conversation id", err))
		return
	}

	var cursor *time.Time
	if cursorParam := c.Query("cursor"); cursorParam != "" {
		t, err := time.Parse(time.RFC3339, cursorParam)
		if err != nil {
			c.Error(apperror.InvalidInput("cursor must be an RFC3339 timestamp", err))
			return
		}
		cursor = &t
	}

	limit := 0
	if limitParam := c.Query("limit"); limitParam != "" {
		n, err := strconv.Atoi(limitParam)
		if err != nil {
			c.Error(apperror.InvalidInput("limit must be an integer", err))
			return
		}
		limit = n
	}

	messages, err := h.uc.ListMessages(c.Request.Context(), orgID, convID, cursor, limit)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]MessageResponse, 0, len(messages))
	for _, m := range messages {
		out = append(out, toMessageResponse(m))
	}
	response.OK(c, out)
}

// TakeOver godoc
// @Summary      Take over a conversation that's pending human handoff
// @Description  Only valid from status=pending_human — see usecase.TakeOver's doc comment.
// @Tags         conversations
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Conversation ID"
// @Success      200 {object} response.Envelope{data=ConversationResponse}
// @Failure      400 {object} response.Envelope
// @Failure      404 {object} response.Envelope
// @Router       /v1/conversations/{id}/take-over [patch]
func (h *ConversationHandler) TakeOver(c *gin.Context) {
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

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid conversation id", err))
		return
	}

	conv, err := h.uc.TakeOver(c.Request.Context(), orgID, id, userID)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toConversationResponse(conv))
}

func toConversationResponse(conv *entity.Conversation) ConversationResponse {
	resp := ConversationResponse{
		ID:                 conv.ID.String(),
		Status:             string(conv.Status),
		CustomerUsername:   conv.CustomerUsername,
		LastMessagePreview: conv.LastMessagePreview,
		UnreadCount:        conv.UnreadCount,
	}
	if conv.LastMessageAt != nil {
		formatted := conv.LastMessageAt.Format(time.RFC3339)
		resp.LastMessageAt = &formatted
	}
	return resp
}

func toMessageResponse(m *entity.Message) MessageResponse {
	return MessageResponse{
		ID:         m.ID.String(),
		Direction:  string(m.Direction),
		SenderType: string(m.SenderType),
		Content:    m.Content,
		CreatedAt:  m.CreatedAt.Format(time.RFC3339),
	}
}
