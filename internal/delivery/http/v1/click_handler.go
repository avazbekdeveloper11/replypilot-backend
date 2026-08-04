package v1

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/click"
)

type ClickHandler struct {
	uc *click.UseCase
}

func NewClickHandler(uc *click.UseCase) *ClickHandler {
	return &ClickHandler{uc: uc}
}

// Get godoc
// @Summary      Get the organization's Click integration status
// @Description  Returns 200 with data=null when Click has never been connected — not a 404. The settings card treats null data as "show the connect form"; distinguishing "not connected" from "not found" as separate error handling isn't worth it for a single per-org row.
// @Tags         integrations
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=ClickIntegrationResponse}
// @Router       /v1/integrations/click [get]
func (h *ClickHandler) Get(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	integration, err := h.uc.Get(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	if integration == nil {
		response.OK(c, nil)
		return
	}
	response.OK(c, toClickIntegrationResponse(integration))
}

// Connect godoc
// @Summary      Connect (or reconnect) Click
// @Description  Stores the org's Click merchant_id/service_id — see entity.ClickIntegration's doc comment on why these are not secrets. Calling this again replaces the existing connection in place.
// @Tags         integrations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body ConnectClickRequest true "Click credentials"
// @Success      200 {object} response.Envelope{data=ClickIntegrationResponse}
// @Router       /v1/integrations/click/connect [post]
func (h *ClickHandler) Connect(c *gin.Context) {
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

	var req ConnectClickRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	integration, err := h.uc.Connect(c.Request.Context(), click.ConnectInput{
		OrganizationID:    orgID,
		MerchantID:        req.MerchantID,
		ServiceID:         req.ServiceID,
		MerchantUserID:    req.MerchantUserID,
		ConnectedByUserID: userID,
	})
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toClickIntegrationResponse(integration))
}

// Disconnect godoc
// @Summary      Disconnect Click
// @Tags         integrations
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=object}
// @Router       /v1/integrations/click/disconnect [post]
func (h *ClickHandler) Disconnect(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	if err := h.uc.Disconnect(c.Request.Context(), orgID); err != nil {
		c.Error(err)
		return
	}
	response.OK(c, gin.H{"disconnected": true})
}

func toClickIntegrationResponse(ci *entity.ClickIntegration) ClickIntegrationResponse {
	return ClickIntegrationResponse{
		MerchantID:     ci.MerchantID,
		ServiceID:      ci.ServiceID,
		MerchantUserID: ci.MerchantUserID,
		ConnectedAt:    ci.CreatedAt.Format(time.RFC3339),
	}
}
