// Package team implements the Team page's backend: list members, invite,
// change role, remove. Deliberately does NOT implement inviting someone
// who has no ReplyPilot account yet — see the doc comment on Invite for
// why, and docs/TEAM_MILESTONE.md for the full scope note.
package team

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

// Member bundles a TeamMember row with the User and Role it references —
// TeamMemberRepository only stores foreign keys (same convention as every
// other repository in this codebase), so the usecase enriches here rather
// than pushing a join into the repository layer.
type Member struct {
	TeamMember *entity.TeamMember
	User       *entity.User
	Role       *entity.Role
}

type UseCase struct {
	memberRepo repository.TeamMemberRepository
	userRepo   repository.UserRepository
	roleRepo   repository.RoleRepository
}

func New(memberRepo repository.TeamMemberRepository, userRepo repository.UserRepository, roleRepo repository.RoleRepository) *UseCase {
	return &UseCase{memberRepo: memberRepo, userRepo: userRepo, roleRepo: roleRepo}
}

// List enriches every membership row for an org with its User and Role.
// N+1 queries (two lookups per member) rather than a SQL join — acceptable
// at team scale (tens of rows, not thousands); revisit if that stops
// being true.
func (uc *UseCase) List(ctx context.Context, orgID uuid.UUID) ([]Member, error) {
	members, err := uc.memberRepo.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	out := make([]Member, 0, len(members))
	for _, m := range members {
		user, err := uc.userRepo.FindByID(ctx, m.UserID)
		if err != nil {
			return nil, err
		}
		role, err := uc.roleRepo.FindByID(ctx, m.RoleID)
		if err != nil {
			return nil, err
		}
		out = append(out, Member{TeamMember: m, User: user, Role: role})
	}
	return out, nil
}

func (uc *UseCase) AssignableRoles(ctx context.Context) ([]*entity.Role, error) {
	return uc.roleRepo.ListSystemRoles(ctx)
}

// Invite only works for an email that already has a ReplyPilot account —
// it links an existing User to this org with the given role. It does NOT
// create an account for a brand-new email, which would need its own
// token-based "set your password" flow (the same shape as
// auth.UseCase.ForgotPassword/ResetPassword, reused rather than
// duplicated — a deliberate, documented follow-up, not an oversight; see
// docs/TEAM_MILESTONE.md). Returns a client-safe, actionable error instead
// of silently doing something more complicated.
func (uc *UseCase) Invite(ctx context.Context, orgID, invitedBy, roleID uuid.UUID, email string) (*Member, error) {
	user, err := uc.userRepo.FindByEmail(ctx, email)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil, apperror.InvalidInput(
				"no ReplyPilot account exists for that email yet — ask them to sign up first, then invite them",
				nil,
			)
		}
		return nil, err
	}

	if _, err := uc.memberRepo.FindByOrganizationAndUser(ctx, orgID, user.ID); err == nil {
		return nil, apperror.Conflict("this person is already a member of this organization")
	} else if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
		return nil, err
	}

	role, err := uc.roleRepo.FindByID(ctx, roleID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	member := &entity.TeamMember{
		OrganizationID: orgID,
		UserID:         user.ID,
		RoleID:         roleID,
		Status:         entity.TeamMemberStatusActive,
		InvitedBy:      &invitedBy,
		InvitedAt:      now,
		JoinedAt:       &now,
	}
	if err := uc.memberRepo.Create(ctx, member); err != nil {
		return nil, err
	}

	return &Member{TeamMember: member, User: user, Role: role}, nil
}

func (uc *UseCase) UpdateRole(ctx context.Context, orgID, memberID, roleID uuid.UUID) (*Member, error) {
	member, err := uc.memberRepo.FindByID(ctx, orgID, memberID)
	if err != nil {
		return nil, err
	}

	role, err := uc.roleRepo.FindByID(ctx, roleID)
	if err != nil {
		return nil, err
	}

	member.RoleID = roleID
	if err := uc.memberRepo.Update(ctx, member); err != nil {
		return nil, err
	}

	user, err := uc.userRepo.FindByID(ctx, member.UserID)
	if err != nil {
		return nil, err
	}

	return &Member{TeamMember: member, User: user, Role: role}, nil
}

// Remove refuses to let someone remove themselves — an org would
// otherwise be able to end up with zero members via this one endpoint,
// including the last Owner. (It does not yet stop the *last remaining*
// member removing a *different* member and leaving the org ownerless in
// other ways — a fuller "can't remove the last Owner" rule is a follow-up,
// not implemented here.)
func (uc *UseCase) Remove(ctx context.Context, orgID, memberID, requestingUserID uuid.UUID) error {
	member, err := uc.memberRepo.FindByID(ctx, orgID, memberID)
	if err != nil {
		return err
	}
	if member.UserID == requestingUserID {
		return apperror.InvalidInput("you can't remove yourself from the organization", nil)
	}
	return uc.memberRepo.Delete(ctx, orgID, memberID)
}
