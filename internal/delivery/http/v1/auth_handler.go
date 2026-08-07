package v1

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/auth"
)

type AuthHandler struct {
	uc *auth.UseCase
}

func NewAuthHandler(uc *auth.UseCase) *AuthHandler {
	return &AuthHandler{uc: uc}
}

// Register godoc
// @Summary      Register a new organization and its Owner user
// @Description  Creates a brand-new organization with the caller as Owner. This is the only signup path with no invite — every subsequent user joins via a team invite.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body RegisterRequest true "Registration payload"
// @Success      201 {object} response.Envelope{data=AuthResponse}
// @Failure      400 {object} response.Envelope
// @Failure      409 {object} response.Envelope
// @Router       /v1/auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	result, err := h.uc.Register(c.Request.Context(), auth.RegisterInput{
		OrganizationName: req.OrganizationName,
		OrganizationSlug: req.OrganizationSlug,
		FullName:         req.FullName,
		Email:            req.Email,
		Password:         req.Password,
		Code:             req.Code,
	})
	if err != nil {
		c.Error(err)
		return
	}

	response.Created(c, toAuthResponse(result))
}

// RequestRegistrationCode godoc
// @Summary      Send a 6-digit email verification code for registration
// @Description  Must be called before Register — Register rejects the request without a valid code. Ensures signups use a real, reachable email address.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body RequestRegistrationCodeRequest true "Email to verify"
// @Success      200 {object} response.Envelope
// @Failure      409 {object} response.Envelope
// @Router       /v1/auth/register/code [post]
func (h *AuthHandler) RequestRegistrationCode(c *gin.Context) {
	var req RequestRegistrationCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	if err := h.uc.RequestRegistrationCode(c.Request.Context(), req.Email); err != nil {
		c.Error(err)
		return
	}

	response.OK(c, gin.H{"message": "verification code sent"})
}

// Login godoc
// @Summary      Log in to a specific organization
// @Description  Authenticates email + password against membership of the given organization_id. A user belonging to multiple orgs picks one per login rather than the API guessing.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body LoginRequest true "Login payload"
// @Success      200 {object} response.Envelope{data=AuthResponse}
// @Failure      401 {object} response.Envelope
// @Failure      403 {object} response.Envelope
// @Router       /v1/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	orgID, err := uuid.Parse(req.OrganizationID)
	if err != nil {
		c.Error(bindError(err))
		return
	}

	result, err := h.uc.Login(c.Request.Context(), auth.LoginInput{
		Email:          req.Email,
		Password:       req.Password,
		OrganizationID: orgID,
	})
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toAuthResponse(result))
}

// Refresh godoc
// @Summary      Exchange a refresh token for a new access token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body RefreshRequest true "Refresh payload"
// @Success      200 {object} response.Envelope{data=AuthResponse}
// @Failure      401 {object} response.Envelope
// @Router       /v1/auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	result, err := h.uc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toAuthResponse(result))
}

// Logout godoc
// @Summary      Revoke a refresh token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body LogoutRequest true "Logout payload"
// @Success      200 {object} response.Envelope
// @Router       /v1/auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	var req LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	if err := h.uc.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		c.Error(err)
		return
	}

	response.OK(c, gin.H{"logged_out": true})
}

// ListOrganizations godoc
// @Summary      List the organizations an email can log into
// @Description  Login requires an organization_id up front; this is the lookup step the frontend calls first so the user can pick a workspace instead of typing a UUID.
// @Tags         auth
// @Produce      json
// @Param        email query string true "Account email"
// @Success      200 {object} response.Envelope{data=[]OrganizationMembershipResponse}
// @Failure      404 {object} response.Envelope
// @Router       /v1/auth/organizations [get]
func (h *AuthHandler) ListOrganizations(c *gin.Context) {
	var q ListOrganizationsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		c.Error(bindError(err))
		return
	}

	memberships, err := h.uc.ListOrganizationsByEmail(c.Request.Context(), q.Email)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]OrganizationMembershipResponse, 0, len(memberships))
	for _, m := range memberships {
		out = append(out, OrganizationMembershipResponse{
			Organization: toOrgResponse(m.Organization),
			MemberStatus: string(m.MemberStatus),
		})
	}
	response.OK(c, out)
}

// ForgotPassword godoc
// @Summary      Request a password reset code
// @Description  Always returns 200 regardless of whether the email is registered — prevents using this endpoint to enumerate accounts. Sends a 6-digit code via email (see internal/platform/notify.EmailSender) rather than a link.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body ForgotPasswordRequest true "Email to send the reset code to"
// @Success      200 {object} response.Envelope
// @Router       /v1/auth/password/forgot [post]
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req ForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	if err := h.uc.ForgotPassword(c.Request.Context(), req.Email); err != nil {
		c.Error(err)
		return
	}

	response.OK(c, gin.H{"message": "if an account exists for that email, a reset code has been sent"})
}

// ResetPassword godoc
// @Summary      Set a new password using an emailed reset code
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body ResetPasswordRequest true "Email + reset code + new password"
// @Success      200 {object} response.Envelope
// @Failure      401 {object} response.Envelope
// @Router       /v1/auth/password/reset [post]
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	if err := h.uc.ResetPassword(c.Request.Context(), req.Email, req.Code, req.NewPassword); err != nil {
		c.Error(err)
		return
	}

	response.OK(c, gin.H{"reset": true})
}

func toAuthResponse(r *auth.Result) AuthResponse {
	return AuthResponse{
		AccessToken:  r.AccessToken,
		RefreshToken: r.RefreshToken,
		ExpiresAt:    r.ExpiresAt.Format(time.RFC3339),
		User:         toUserResponse(r.User),
		Organization: toOrgResponse(r.Organization),
	}
}

func toOrgResponse(o *entity.Organization) OrgResponse {
	return OrgResponse{
		ID:                   o.ID.String(),
		Name:                 o.Name,
		Slug:                 o.Slug,
		Status:               string(o.Status),
		Timezone:             o.Timezone,
		BusinessHoursEnabled: o.BusinessHoursEnabled,
		BusinessHoursStart:   formatClockMinutes(o.BusinessHoursStartMinutes),
		BusinessHoursEnd:     formatClockMinutes(o.BusinessHoursEndMinutes),
	}
}

// formatClockMinutes renders minutes-since-midnight back to "HH:MM",
// mirroring organization.businessHoursTimeLayout on the way in. Returns
// nil for nil input so OrgResponse's omitempty fields disappear entirely
// rather than serializing as a misleading "00:00".
func formatClockMinutes(minutes *int) *string {
	if minutes == nil {
		return nil
	}
	formatted := fmt.Sprintf("%02d:%02d", *minutes/60, *minutes%60)
	return &formatted
}
