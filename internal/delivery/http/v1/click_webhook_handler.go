package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/integration/clickapi"
	"github.com/replypilot/backend/internal/usecase/payment"
)

type ClickWebhookHandler struct {
	uc *payment.WebhookUseCase
}

func NewClickWebhookHandler(uc *payment.WebhookUseCase) *ClickWebhookHandler {
	return &ClickWebhookHandler{uc: uc}
}

// Receive godoc
// @Summary      Receive a Click (click.uz) Prepare/Complete payment callback
// @Description  Single shared endpoint for both phases of Click's Shop API webhook, distinguished by the `action` form field — see payment.WebhookUseCase.Process's doc comment. Always responds 200 with a Click-shaped JSON body (error/error_note); Click's own protocol communicates failure through that body, not the HTTP status, so this never writes a non-200 for a well-formed request.
// @Tags         webhooks
// @Accept       x-www-form-urlencoded
// @Produce      json
// @Success      200 {object} object
// @Router       /webhooks/click [post]
func (h *ClickWebhookHandler) Receive(c *gin.Context) {
	var req clickapi.WebhookRequest
	if err := c.ShouldBind(&req); err != nil {
		// Malformed enough that we can't even echo click_trans_id/merchant_trans_id
		// back — this is the one case Click's own protocol has no error code
		// for, since every documented code assumes the base fields parsed.
		c.JSON(http.StatusOK, gin.H{"error": clickapi.ErrBadRequest, "error_note": "Error in request from click"})
		return
	}

	c.JSON(http.StatusOK, h.uc.Process(c.Request.Context(), req))
}
