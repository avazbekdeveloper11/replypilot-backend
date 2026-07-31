package v1

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/usecase/instagram"
)

type WebhookHandler struct {
	uc *instagram.WebhookUseCase
}

func NewWebhookHandler(uc *instagram.WebhookUseCase) *WebhookHandler {
	return &WebhookHandler{uc: uc}
}

// VerifySubscription godoc
// @Summary      Meta webhook subscription handshake
// @Description  Called once by Meta when the webhook URL is configured in the App Dashboard. Must echo hub.challenge back unmodified after validating hub.verify_token — this is what proves the endpoint belongs to you.
// @Tags         webhooks
// @Produce      plain
// @Param        hub.mode         query string true "subscribe"
// @Param        hub.verify_token query string true "shared secret configured in the Meta App Dashboard"
// @Param        hub.challenge    query string true "value to echo back"
// @Success      200 {string} string "the challenge value"
// @Failure      401 {string} string "verification failed"
// @Router       /webhooks/meta [get]
func (h *WebhookHandler) VerifySubscription(c *gin.Context) {
	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")
	challenge := c.Query("hub.challenge")

	result, err := h.uc.VerifySubscription(mode, token, challenge)
	if err != nil {
		c.String(http.StatusUnauthorized, "verification failed")
		return
	}

	c.String(http.StatusOK, result)
}

// Receive godoc
// @Summary      Receive an Instagram DM webhook delivery
// @Description  Verifies X-Hub-Signature-256 against the raw request body, logs the delivery unconditionally (see entity.WebhookLog), and for a validly-signed, recognized payload persists the inbound message idempotently and publishes it for AI processing. Always returns 200 on a validly-signed request, per Meta's requirements — an endpoint that returns non-2xx repeatedly gets throttled or unsubscribed.
// @Tags         webhooks
// @Accept       json
// @Produce      json
// @Success      200 {object} response.Envelope
// @Failure      401 {object} response.Envelope
// @Router       /webhooks/meta [post]
func (h *WebhookHandler) Receive(c *gin.Context) {
	// Read the raw body BEFORE any binding/parsing — signature verification
	// is over the exact bytes Meta sent, and c.ShouldBindJSON would consume
	// the stream and re-serialize differently.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	signatureHeader := c.GetHeader("X-Hub-Signature-256")

	if err := h.uc.Process(c.Request.Context(), signatureHeader, body); err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}
