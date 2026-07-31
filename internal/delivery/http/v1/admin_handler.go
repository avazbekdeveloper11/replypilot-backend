package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/usecase/admin"
	"github.com/replypilot/backend/internal/usecase/platformsettings"
)

// AdminHandler serves /v1/admin/* — every route here is gated by the
// RequirePlatformAdmin middleware (see router.go), not by anything in
// this file itself. It has no orgIDFromContext calls anywhere — that's
// deliberate, these routes are cross-tenant by design.
//
// Two usecases, not one: uc (organizations/stats) and settings (platform-
// wide secrets like the Gemini key) are unrelated concerns that happen to
// share the same authorization boundary. See platformsettings' package
// doc comment for why that one isn't just folded into usecase/admin.
type AdminHandler struct {
	uc       *admin.UseCase
	settings *platformsettings.UseCase
}

func NewAdminHandler(uc *admin.UseCase, settings *platformsettings.UseCase) *AdminHandler {
	return &AdminHandler{uc: uc, settings: settings}
}

// ListOrganizations godoc
// @Summary      List every organization (platform admin only)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]AdminOrganizationResponse}
// @Failure      403 {object} response.Envelope
// @Router       /v1/admin/organizations [get]
func (h *AdminHandler) ListOrganizations(c *gin.Context) {
	summaries, err := h.uc.ListOrganizations(c.Request.Context())
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]AdminOrganizationResponse, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, toAdminOrganizationResponse(s))
	}
	response.OK(c, out)
}

// Suspend godoc
// @Summary      Suspend an organization's access (platform admin only)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Organization ID"
// @Success      200 {object} response.Envelope{data=OrgResponse}
// @Router       /v1/admin/organizations/{id}/suspend [post]
func (h *AdminHandler) Suspend(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid organization id", err))
		return
	}
	org, err := h.uc.SuspendOrganization(c.Request.Context(), id)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toOrgResponse(org))
}

// Reactivate godoc
// @Summary      Reactivate a suspended organization (platform admin only)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Organization ID"
// @Success      200 {object} response.Envelope{data=OrgResponse}
// @Router       /v1/admin/organizations/{id}/reactivate [post]
func (h *AdminHandler) Reactivate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid organization id", err))
		return
	}
	org, err := h.uc.ReactivateOrganization(c.Request.Context(), id)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toOrgResponse(org))
}

// Stats godoc
// @Summary      Platform-wide aggregate stats (platform admin only)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=AdminPlatformStatsResponse}
// @Router       /v1/admin/stats [get]
func (h *AdminHandler) Stats(c *gin.Context) {
	stats, err := h.uc.Stats(c.Request.Context())
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toAdminPlatformStatsResponse(stats))
}

// GetGeminiSettings godoc
// @Summary      Get Gemini API key configuration status (platform admin only)
// @Description  Never returns the key itself — only whether one is configured and when it was last changed. See platformsettings.GeminiKeyStatus.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=AdminGeminiSettingsResponse}
// @Router       /v1/admin/settings/gemini [get]
func (h *AdminHandler) GetGeminiSettings(c *gin.Context) {
	status, err := h.settings.Status(c.Request.Context())
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, AdminGeminiSettingsResponse{
		Configured: status.Configured,
		UpdatedAt:  status.UpdatedAt,
	})
}

// SetGeminiSettings godoc
// @Summary      Set or rotate the Gemini API key (platform admin only)
// @Description  Both API service and worker-ai processes pick up a changed key within internal/platform/geminikey's poll interval — no redeploy needed. See that package's doc comment.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body SetAdminGeminiSettingsRequest true "New Gemini API key"
// @Success      200 {object} response.Envelope{data=AdminGeminiSettingsResponse}
// @Router       /v1/admin/settings/gemini [put]
func (h *AdminHandler) SetGeminiSettings(c *gin.Context) {
	var req SetAdminGeminiSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperror.InvalidInput("invalid request body", err))
		return
	}

	userID, err := userIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	if err := h.settings.SetGeminiAPIKey(c.Request.Context(), req.APIKey, userID); err != nil {
		c.Error(err)
		return
	}

	status, err := h.settings.Status(c.Request.Context())
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, AdminGeminiSettingsResponse{
		Configured: status.Configured,
		UpdatedAt:  status.UpdatedAt,
	})
}

func toAdminOrganizationResponse(s repository.OrganizationSummary) AdminOrganizationResponse {
	return AdminOrganizationResponse{
		Organization:       toOrgResponse(s.Organization),
		MemberCount:        s.MemberCount,
		PlanCode:           s.PlanCode,
		SubscriptionStatus: s.SubscriptionStatus,
	}
}

func toAdminPlatformStatsResponse(s *repository.PlatformStats) AdminPlatformStatsResponse {
	byPlan := make([]AdminPlanSubscriptionCountResponse, 0, len(s.SubscriptionsByPlan))
	for _, p := range s.SubscriptionsByPlan {
		byPlan = append(byPlan, AdminPlanSubscriptionCountResponse{
			PlanCode: p.PlanCode,
			PlanName: p.PlanName,
			Count:    p.Count,
		})
	}
	return AdminPlatformStatsResponse{
		TotalOrganizations:  s.TotalOrganizations,
		TotalUsers:          s.TotalUsers,
		TotalConversations:  s.TotalConversations,
		TotalMessages:       s.TotalMessages,
		ActiveSubscriptions: s.ActiveSubscriptions,
		MRRCentsApprox:      s.MRRCentsApprox,
		SubscriptionsByPlan: byPlan,
	}
}
