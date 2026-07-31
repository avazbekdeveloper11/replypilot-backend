package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type TeamMemberRepository interface {
	Create(ctx context.Context, member *entity.TeamMember) error
	FindByOrganizationAndUser(ctx context.Context, orgID, userID uuid.UUID) (*entity.TeamMember, error)

	// ListByUserID returns every membership row for a user ACROSS all
	// organizations — the one query in this repository that doesn't (can't)
	// scope by a single org_id, because the caller doesn't know which org
	// yet; discovering that is the point (e.g. "which orgs can this email
	// log into"). See the postgres implementation's doc comment for the
	// RLS exception this requires — same shape as
	// InstagramAccountRepository.FindByIGUserID.
	ListByUserID(ctx context.Context, userID uuid.UUID) ([]*entity.TeamMember, error)

	// ListByOrganization backs the Team page — every member row for one
	// tenant, newest-invited first. Small N per org (teams, not
	// customers), so no pagination here unlike ConversationRepository.List.
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.TeamMember, error)
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TeamMember, error)
	Update(ctx context.Context, member *entity.TeamMember) error
	// Delete is a soft delete (GORM's standard deleted_at mechanism) —
	// removing someone from a team shouldn't erase the historical record
	// of who was on it and when.
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}
