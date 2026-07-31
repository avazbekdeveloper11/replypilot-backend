package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type TeamMemberRepository struct {
	db *gorm.DB
}

func NewTeamMemberRepository(db *gorm.DB) *TeamMemberRepository {
	return &TeamMemberRepository{db: db}
}

func (r *TeamMemberRepository) Create(ctx context.Context, member *entity.TeamMember) error {
	model := teamMemberToModel(member)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}
	if model.InvitedAt.IsZero() {
		model.InvitedAt = time.Now()
	}

	err := withTenant(ctx, r.db, member.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create team member", err)
	}

	*member = *modelToTeamMember(model)
	return nil
}

func (r *TeamMemberRepository) FindByOrganizationAndUser(ctx context.Context, orgID, userID uuid.UUID) (*entity.TeamMember, error) {
	var model TeamMemberModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ? AND user_id = ?", orgID, userID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("team member not found")
		}
		return nil, apperror.Internal("find team member", err)
	}
	return modelToTeamMember(&model), nil
}

// ListByUserID deliberately does NOT go through withTenant: it exists to
// answer "which organizations does this user belong to", called from the
// login-discovery path (auth.UseCase.ListOrganizationsByEmail) before any
// tenant context exists — there is no single org_id to scope by, that's
// exactly what this call produces. Same exception, same shape, as
// InstagramAccountRepository.FindByIGUserID for the webhook path.
//
// Under the standard tenant_isolation policy alone this would return zero
// rows. Migration 000004 adds a permissive SELECT-only policy
// (member_email_lookup) that allows the read when the session GUC
// app.member_lookup is 'on'. This method opts in by setting that GUC with
// SET LOCAL inside a transaction, so the elevated read is scoped to
// exactly this one query and auto-clears on commit.
func (r *TeamMemberRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]*entity.TeamMember, error) {
	var models []TeamMemberModel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL app.member_lookup = 'on'").Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list team members by user id", err)
	}

	members := make([]*entity.TeamMember, 0, len(models))
	for i := range models {
		members = append(members, modelToTeamMember(&models[i]))
	}
	return members, nil
}

func (r *TeamMemberRepository) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.TeamMember, error) {
	var models []TeamMemberModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).Order("invited_at DESC").Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list team members by organization", err)
	}

	members := make([]*entity.TeamMember, 0, len(models))
	for i := range models {
		members = append(members, modelToTeamMember(&models[i]))
	}
	return members, nil
}

func (r *TeamMemberRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TeamMember, error) {
	var model TeamMemberModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id = ? AND organization_id = ?", id, orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("team member not found")
		}
		return nil, apperror.Internal("find team member by id", err)
	}
	return modelToTeamMember(&model), nil
}

func (r *TeamMemberRepository) Update(ctx context.Context, member *entity.TeamMember) error {
	model := teamMemberToModel(member)
	var rowsAffected int64
	err := withTenant(ctx, r.db, member.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&TeamMemberModel{}).Where("id = ?", member.ID).Updates(model)
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update team member", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("team member not found")
	}
	return nil
}

func (r *TeamMemberRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND organization_id = ?", id, orgID).Delete(&TeamMemberModel{})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("delete team member", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("team member not found")
	}
	return nil
}

func teamMemberToModel(t *entity.TeamMember) *TeamMemberModel {
	return &TeamMemberModel{
		ID:             t.ID,
		OrganizationID: t.OrganizationID,
		UserID:         t.UserID,
		RoleID:         t.RoleID,
		Status:         string(t.Status),
		InvitedBy:      t.InvitedBy,
		InvitedAt:      t.InvitedAt,
		JoinedAt:       t.JoinedAt,
	}
}

func modelToTeamMember(m *TeamMemberModel) *entity.TeamMember {
	e := &entity.TeamMember{
		ID:             m.ID,
		OrganizationID: m.OrganizationID,
		UserID:         m.UserID,
		RoleID:         m.RoleID,
		Status:         entity.TeamMemberStatus(m.Status),
		InvitedBy:      m.InvitedBy,
		InvitedAt:      m.InvitedAt,
		JoinedAt:       m.JoinedAt,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
