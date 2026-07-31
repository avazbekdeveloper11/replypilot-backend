package v1

import (
	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/user"
)

type UserHandler struct {
	uc *user.UseCase
}

func NewUserHandler(uc *user.UseCase) *UserHandler {
	return &UserHandler{uc: uc}
}

// Me godoc
// @Summary      Get the current user's own profile
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=UserResponse}
// @Router       /v1/users/me [get]
func (h *UserHandler) Me(c *gin.Context) {
	userID, err := userIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	u, err := h.uc.Me(c.Request.Context(), userID)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toUserResponse(u))
}

// UpdateProfile godoc
// @Summary      Update the current user's own profile
// @Description  full_name and avatar_url only — email changes aren't supported (see usecase/user's doc comment).
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body UpdateProfileRequest true "Profile fields"
// @Success      200 {object} response.Envelope{data=UserResponse}
// @Router       /v1/users/me [patch]
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	userID, err := userIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	u, err := h.uc.UpdateProfile(c.Request.Context(), userID, req.FullName, req.AvatarURL)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toUserResponse(u))
}

// ChangePassword godoc
// @Summary      Change the current user's own password
// @Description  Requires the current password — see usecase/user.UseCase.ChangePassword's doc comment on why this differs from the forgot-password flow.
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body ChangePasswordRequest true "Current and new password"
// @Success      200 {object} response.Envelope{data=object}
// @Failure      401 {object} response.Envelope
// @Router       /v1/users/me/change-password [post]
func (h *UserHandler) ChangePassword(c *gin.Context) {
	userID, err := userIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}

	if err := h.uc.ChangePassword(c.Request.Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
		c.Error(err)
		return
	}
	response.OK(c, gin.H{"changed": true})
}

func toUserResponse(u *entity.User) UserResponse {
	return UserResponse{
		ID:              u.ID.String(),
		Email:           u.Email,
		FullName:        u.FullName,
		AvatarURL:       u.AvatarURL,
		IsPlatformAdmin: u.IsPlatformAdmin,
	}
}
