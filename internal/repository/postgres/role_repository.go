package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type RoleRepository struct {
	db *gorm.DB
}

func NewRoleRepository(db *gorm.DB) *RoleRepository {
	return &RoleRepository{db: db}
}

func (r *RoleRepository) FindByID(ctx context.Context, id uuid.UUID) (*entity.Role, error) {
	var model RoleModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("role not found")
		}
		return nil, apperror.Internal("find role by id", err)
	}
	return modelToRole(&model), nil
}

func (r *RoleRepository) FindSystemRoleByName(ctx context.Context, name string) (*entity.Role, error) {
	var model RoleModel
	err := r.db.WithContext(ctx).
		Where("organization_id IS NULL AND name = ?", name).
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("system role not found: " + name)
		}
		return nil, apperror.Internal("find system role by name", err)
	}
	return modelToRole(&model), nil
}

func (r *RoleRepository) ListSystemRoles(ctx context.Context) ([]*entity.Role, error) {
	var models []RoleModel
	err := r.db.WithContext(ctx).
		Where("organization_id IS NULL").
		Order("name ASC").
		Find(&models).Error
	if err != nil {
		return nil, apperror.Internal("list system roles", err)
	}

	roles := make([]*entity.Role, 0, len(models))
	for i := range models {
		roles = append(roles, modelToRole(&models[i]))
	}
	return roles, nil
}

func modelToRole(m *RoleModel) *entity.Role {
	e := &entity.Role{
		ID:             m.ID,
		OrganizationID: m.OrganizationID,
		Name:           m.Name,
		Description:    m.Description,
		IsSystem:       m.IsSystem,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
