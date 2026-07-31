package v1

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/usecase/team"
)

type TeamHandler struct {
	uc *team.UseCase
}

func NewTeamHandler(uc *team.UseCase) *TeamHandler {
	return &TeamHandler{uc: uc}
}

// List godoc
// @Summary      List team members for the current organization
// @Tags         team
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]TeamMemberResponse}
// @Router       /v1/team/members [get]
func (h *TeamHandler) List(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	members, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]TeamMemberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, toTeamMemberResponse(m))
	}
	response.OK(c, out)
}

// Roles godoc
// @Summary      List assignable roles
// @Tags         team
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=[]RoleResponse}
// @Router       /v1/team/roles [get]
func (h *TeamHandler) Roles(c *gin.Context) {
	roles, err := h.uc.AssignableRoles(c.Request.Context())
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]RoleResponse, 0, len(roles))
	for _, r := range roles {
		out = append(out, RoleResponse{ID: r.ID.String(), Name: r.Name})
	}
	response.OK(c, out)
}

// Invite godoc
// @Summary      Invite an existing ReplyPilot user to this organization
// @Description  Only works for an email that already has an account — see the usecase doc comment for why.
// @Tags         team
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request body InviteMemberRequest true "Invite payload"
// @Success      201 {object} response.Envelope{data=TeamMemberResponse}
// @Router       /v1/team/members [post]
func (h *TeamHandler) Invite(c *gin.Context) {
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

	var req InviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}
	roleID, err := uuid.Parse(req.RoleID)
	if err != nil {
		c.Error(apperror.InvalidInput("invalid role id", err))
		return
	}

	member, err := h.uc.Invite(c.Request.Context(), orgID, userID, roleID, req.Email)
	if err != nil {
		c.Error(err)
		return
	}

	response.Created(c, toTeamMemberResponse(*member))
}

// UpdateRole godoc
// @Summary      Change a team member's role
// @Tags         team
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Team member ID"
// @Param        request body UpdateMemberRoleRequest true "New role"
// @Success      200 {object} response.Envelope{data=TeamMemberResponse}
// @Router       /v1/team/members/{id} [patch]
func (h *TeamHandler) UpdateRole(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	memberID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid team member id", err))
		return
	}

	var req UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(bindError(err))
		return
	}
	roleID, err := uuid.Parse(req.RoleID)
	if err != nil {
		c.Error(apperror.InvalidInput("invalid role id", err))
		return
	}

	member, err := h.uc.UpdateRole(c.Request.Context(), orgID, memberID, roleID)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, toTeamMemberResponse(*member))
}

// Remove godoc
// @Summary      Remove a team member
// @Description  Returns a normal {data} envelope, not a bare 204 — every client in this codebase (goApiFetch, apiFetch) unconditionally parses a JSON body, and a truly empty 204 response would make those throw a spurious "non-JSON response" error despite the removal succeeding.
// @Tags         team
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Team member ID"
// @Success      200 {object} response.Envelope{data=object}
// @Router       /v1/team/members/{id} [delete]
func (h *TeamHandler) Remove(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	requestingUserID, err := userIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	memberID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(apperror.InvalidInput("invalid team member id", err))
		return
	}

	if err := h.uc.Remove(c.Request.Context(), orgID, memberID, requestingUserID); err != nil {
		c.Error(err)
		return
	}

	response.OK(c, gin.H{"removed": true})
}

func toTeamMemberResponse(m team.Member) TeamMemberResponse {
	resp := TeamMemberResponse{
		ID: m.TeamMember.ID.String(),
		User: UserResponse{
			ID:       m.User.ID.String(),
			Email:    m.User.Email,
			FullName: m.User.FullName,
		},
		Role:      RoleResponse{ID: m.Role.ID.String(), Name: m.Role.Name},
		Status:    string(m.TeamMember.Status),
		InvitedAt: m.TeamMember.InvitedAt.Format(time.RFC3339),
	}
	if m.TeamMember.JoinedAt != nil {
		joined := m.TeamMember.JoinedAt.Format(time.RFC3339)
		resp.JoinedAt = &joined
	}
	return resp
}
