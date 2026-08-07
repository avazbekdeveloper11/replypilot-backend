package v1

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/amocrm"
)

type AmoCRMHandler struct {
	oauth *amocrm.OAuthUseCase
	sync  *amocrm.SyncUseCase
}

func NewAmoCRMHandler(oauth *amocrm.OAuthUseCase, sync *amocrm.SyncUseCase) *AmoCRMHandler {
	return &AmoCRMHandler{oauth: oauth, sync: sync}
}

// Connect godoc
// @Summary      Start the amoCRM OAuth connect flow
// @Description  Generates a single-use CSRF state and returns amoCRM's authorization URL to redirect the user to — same shape as InstagramHandler.Connect.
// @Tags         amocrm
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=AmoCRMConnectResponse}
// @Router       /v1/amocrm/connect [post]
func (h *AmoCRMHandler) Connect(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	authURL, state, err := h.oauth.StartConnect(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, AmoCRMConnectResponse{
		AuthorizationURL: authURL,
		State:            state,
	})
}

// Callback godoc
// @Summary      Complete the amoCRM OAuth connect flow
// @Description  Verifies the CSRF `state` and exchanges the authorization code. The organization is taken from the stored state, not the request — safe whether called by an authenticated dashboard request or proxied from amoCRM's own redirect. `referer` is amoCRM's own GET param carrying the subdomain the user connected — required, since amoCRM never asks for it up front.
// @Tags         amocrm
// @Produce      json
// @Param        code    query string true "Authorization code from amoCRM"
// @Param        state   query string true "CSRF state returned by /connect"
// @Param        referer query string true "amoCRM subdomain, e.g. example.amocrm.ru"
// @Success      200 {object} response.Envelope{data=AmoCRMIntegrationResponse}
// @Failure      400 {object} response.Envelope
// @Failure      401 {object} response.Envelope
// @Router       /v1/amocrm/callback [get]
func (h *AmoCRMHandler) Callback(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.Error(apperror.InvalidInput("missing code query parameter", nil))
		return
	}
	state := c.Query("state")
	referer := c.Query("referer")

	integration, err := h.oauth.Complete(c.Request.Context(), state, code, referer)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toAmoCRMIntegrationResponse(integration))
}

// Status godoc
// @Summary      Get the organization's amoCRM connection status
// @Description  Returns 200 with data=null when amoCRM has never been connected — not a 404, same convention as ClickHandler.Get.
// @Tags         amocrm
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=AmoCRMIntegrationResponse}
// @Router       /v1/integrations/amocrm [get]
func (h *AmoCRMHandler) Status(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	integration, err := h.oauth.Get(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	if integration == nil {
		response.OK(c, nil)
		return
	}
	response.OK(c, toAmoCRMIntegrationResponse(integration))
}

// Disconnect godoc
// @Summary      Disconnect amoCRM
// @Description  Removes ReplyPilot's stored access only — does not revoke the grant on amoCRM's side (no such per-token revoke endpoint is exposed to apps; the org's own amoCRM admin can also remove the integration from Settings -> Integrations -> Installed).
// @Tags         amocrm
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=object}
// @Router       /v1/integrations/amocrm/disconnect [post]
func (h *AmoCRMHandler) Disconnect(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	if err := h.oauth.Disconnect(c.Request.Context(), orgID); err != nil {
		c.Error(err)
		return
	}
	response.OK(c, gin.H{"disconnected": true})
}

// Sync godoc
// @Summary      Push one customer to amoCRM
// @Description  Creates (first sync) or updates (every sync after) an amoCRM contact for this conversation's customer, plus a note summarizing their paid-order history — see amocrm.SyncUseCase.SyncCustomer's doc comment for this feature's exact one-way, on-demand scope.
// @Tags         amocrm
// @Produce      json
// @Security     BearerAuth
// @Param        conversation_id path string true "Conversation id"
// @Success      200 {object} response.Envelope{data=AmoCRMSyncResponse}
// @Router       /v1/customers/{conversation_id}/amocrm-sync [post]
func (h *AmoCRMHandler) Sync(c *gin.Context) {
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

	link, err := h.sync.SyncCustomer(c.Request.Context(), orgID, conversationID)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toAmoCRMSyncResponse(link))
}

func toAmoCRMIntegrationResponse(i *entity.AmoCRMIntegration) AmoCRMIntegrationResponse {
	return AmoCRMIntegrationResponse{
		Subdomain:   i.Subdomain,
		Status:      string(i.Status),
		ConnectedAt: i.CreatedAt.Format(time.RFC3339),
	}
}

func toAmoCRMSyncResponse(l *entity.AmoCRMContactLink) AmoCRMSyncResponse {
	return AmoCRMSyncResponse{
		AmoCRMContactID: l.AmoCRMContactID,
		SyncedAt:        l.SyncedAt.Format(time.RFC3339),
	}
}
