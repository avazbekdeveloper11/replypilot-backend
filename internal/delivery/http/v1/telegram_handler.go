package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/telegram"
)

type TelegramHandler struct {
	connect *telegram.ConnectUseCase
}

func NewTelegramHandler(connect *telegram.ConnectUseCase) *TelegramHandler {
	return &TelegramHandler{connect: connect}
}

// Connect godoc
// @Summary      Connect a Telegram bot
// @Description  Validates the bot token against Telegram and registers this service's webhook for it. See entity.TelegramAccount's doc comment for the pairing step this does NOT do (that happens inside the org's own Telegram app, not here).
// @Tags         telegram
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body TelegramConnectRequest true "Bot token from @BotFather"
// @Success      200 {object} response.Envelope{data=TelegramAccountResponse}
// @Failure      400 {object} response.Envelope
// @Router       /v1/telegram/connect [post]
func (h *TelegramHandler) Connect(c *gin.Context) {
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

	var req TelegramConnectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	account, err := h.connect.Connect(c.Request.Context(), telegram.ConnectInput{
		OrganizationID:    orgID,
		BotToken:          req.BotToken,
		ConnectedByUserID: userID,
	})
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toTelegramAccountResponse(account))
}

// List godoc
// @Summary      List connected Telegram bots for the current organization
// @Tags         telegram
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]TelegramAccountResponse}
// @Router       /v1/telegram/accounts [get]
func (h *TelegramHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	accounts, err := h.connect.ListForOrganization(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]TelegramAccountResponse, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, toTelegramAccountResponse(a))
	}
	response.OK(c, out)
}

// Disconnect godoc
// @Summary      Disconnect a Telegram bot
// @Tags         telegram
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Telegram account ID"
// @Success      200 {object} response.Envelope{data=object}
// @Router       /v1/telegram/accounts/{id} [delete]
func (h *TelegramHandler) Disconnect(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid telegram account id", err))
		return
	}

	if err := h.connect.Disconnect(c.Request.Context(), orgID, id); err != nil {
		c.Error(err)
		return
	}

	response.OK(c, gin.H{"disconnected": true})
}

// GenerateNotifyCode godoc
// @Summary      Generate a Telegram admin-notification verification code
// @Description  Returns a fresh one-time code; the admin sends it as a plain message to the connected bot to bind their chat for lead/payment notifications. See telegram.ConnectUseCase.GenerateNotifyCode.
// @Tags         telegram
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Telegram account ID"
// @Success      200 {object} response.Envelope{data=TelegramNotifyCodeResponse}
// @Router       /v1/telegram/accounts/{id}/notify-code [post]
func (h *TelegramHandler) GenerateNotifyCode(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid telegram account id", err))
		return
	}

	code, err := h.connect.GenerateNotifyCode(c.Request.Context(), orgID, id)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, TelegramNotifyCodeResponse{Code: code})
}

// UpdateNotifySettings godoc
// @Summary      Toggle Telegram admin lead/payment notifications
// @Tags         telegram
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Telegram account ID"
// @Param        request body TelegramNotifySettingsRequest true "Notification toggles"
// @Success      200 {object} response.Envelope{data=TelegramAccountResponse}
// @Router       /v1/telegram/accounts/{id}/notify-settings [patch]
func (h *TelegramHandler) UpdateNotifySettings(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid telegram account id", err))
		return
	}

	var req TelegramNotifySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	account, err := h.connect.UpdateNotifySettings(c.Request.Context(), orgID, id, req.NotifyOnLead, req.NotifyOnPayment)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toTelegramAccountResponse(account))
}

func toTelegramAccountResponse(a *entity.TelegramAccount) TelegramAccountResponse {
	return TelegramAccountResponse{
		ID:              a.ID.String(),
		Username:        a.BotUsername,
		Status:          string(a.Status),
		Paired:          a.BusinessConnectionID != nil,
		NotifyVerified:  a.NotifyChatID != nil,
		NotifyOnLead:    a.NotifyOnLead,
		NotifyOnPayment: a.NotifyOnPayment,
	}
}
