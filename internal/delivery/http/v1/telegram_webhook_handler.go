package v1

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/usecase/telegram"
)

type TelegramWebhookHandler struct {
	uc *telegram.WebhookUseCase
}

func NewTelegramWebhookHandler(uc *telegram.WebhookUseCase) *TelegramWebhookHandler {
	return &TelegramWebhookHandler{uc: uc}
}

// Receive godoc
// @Summary      Receive a Telegram Business Bot webhook delivery
// @Description  Verifies the X-Telegram-Bot-Api-Secret-Token header, logs the delivery unconditionally (see entity.WebhookLog), and for a business_connection or business_message update processes it. Always returns 200 for a correctly-secreted request — Telegram disables a webhook that repeatedly errors, same reasoning as instagram.WebhookHandler.Receive's identical doc comment.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Param        id path string true "Telegram account ID (from the URL registered via setWebhook)"
// @Success      200 {object} response.Envelope
// @Failure      401 {object} response.Envelope
// @Router       /webhooks/telegram/{id} [post]
func (h *TelegramWebhookHandler) Receive(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	if !h.uc.VerifySecret(c.GetHeader("X-Telegram-Bot-Api-Secret-Token")) {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	if err := h.uc.Process(c.Request.Context(), id, body); err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}
