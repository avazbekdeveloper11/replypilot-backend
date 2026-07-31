package v1

import (
	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/usecase/organization"
)

type OrganizationHandler struct {
	uc *organization.UseCase
}

func NewOrganizationHandler(uc *organization.UseCase) *OrganizationHandler {
	return &OrganizationHandler{uc: uc}
}

// Me godoc
// @Summary      Get the current organization
// @Tags         organizations
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=OrgResponse}
// @Failure      401 {object} response.Envelope
// @Router       /v1/organizations/me [get]
func (h *OrganizationHandler) Me(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	org, err := h.uc.GetByID(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toOrgResponse(org))
}

// UpdateMe godoc
// @Summary      Update the current organization's settings
// @Description  Name and timezone only — slug is not editable here, see usecase/organization's doc comment.
// @Tags         organizations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body UpdateOrganizationRequest true "Settings fields"
// @Success      200 {object} response.Envelope{data=OrgResponse}
// @Router       /v1/organizations/me [patch]
func (h *OrganizationHandler) UpdateMe(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req UpdateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	org, err := h.uc.UpdateSettings(c.Request.Context(), orgID, req.Name, req.Timezone)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toOrgResponse(org))
}
