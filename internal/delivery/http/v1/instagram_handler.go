package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/instagram"
)

type InstagramHandler struct {
	oauth *instagram.OAuthUseCase
}

func NewInstagramHandler(oauth *instagram.OAuthUseCase) *InstagramHandler {
	return &InstagramHandler{oauth: oauth}
}

// Connect godoc
// @Summary      Start the Instagram OAuth connect flow
// @Description  Generates a single-use CSRF state (stored server-side, bound to the caller's organization) and returns the Meta authorization URL to redirect the user to. The state is verified on callback — the client does not need to store or echo it, but the returned `state` is included for clients that want to double-check.
// @Tags         instagram
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=InstagramConnectResponse}
// @Router       /v1/instagram/connect [post]
func (h *InstagramHandler) Connect(c *gin.Context) {
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

	response.OK(c, InstagramConnectResponse{
		AuthorizationURL: authURL,
		State:            state,
	})
}

// Callback godoc
// @Summary      Complete the Instagram OAuth connect flow
// @Description  Verifies the CSRF `state` against the server-side store and, on success, completes the token exchange and webhook subscription. The organization is taken from the stored state, not from the request — so this endpoint is safe whether it's called by an authenticated dashboard request or proxied from Meta's browser redirect.
// @Tags         instagram
// @Produce      json
// @Param        code  query string true "Authorization code from Meta"
// @Param        state query string true "CSRF state returned by /connect"
// @Success      200 {object} response.Envelope{data=InstagramAccountResponse}
// @Failure      400 {object} response.Envelope
// @Failure      401 {object} response.Envelope
// @Router       /v1/instagram/callback [get]
func (h *InstagramHandler) Callback(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.Error(apperror.InvalidInput("missing code query parameter", nil))
		return
	}
	state := c.Query("state")

	account, err := h.oauth.Complete(c.Request.Context(), state, code)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toInstagramAccountResponse(account))
}

// List godoc
// @Summary      List connected Instagram accounts for the current organization
// @Tags         instagram
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]InstagramAccountResponse}
// @Router       /v1/instagram/accounts [get]
func (h *InstagramHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	accounts, err := h.oauth.ListForOrganization(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]InstagramAccountResponse, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, toInstagramAccountResponse(a))
	}
	response.OK(c, out)
}

// Disconnect godoc
// @Summary      Disconnect an Instagram account
// @Description  Removes ReplyPilot's stored access only — does not revoke the grant on Instagram's side. See OAuthUseCase.Disconnect's doc comment.
// @Tags         instagram
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Instagram account ID"
// @Success      200 {object} response.Envelope{data=object}
// @Router       /v1/instagram/accounts/{id} [delete]
func (h *InstagramHandler) Disconnect(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid instagram account id", err))
		return
	}

	if err := h.oauth.Disconnect(c.Request.Context(), orgID, id); err != nil {
		c.Error(err)
		return
	}

	response.OK(c, gin.H{"disconnected": true})
}

func toInstagramAccountResponse(a *entity.InstagramAccount) InstagramAccountResponse {
	return InstagramAccountResponse{
		ID:       a.ID.String(),
		Username: a.Username,
		Status:   string(a.Status),
	}
}
