package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type OrganizationRepository struct {
	db *gorm.DB
}

func NewOrganizationRepository(db *gorm.DB) *OrganizationRepository {
	return &OrganizationRepository{db: db}
}

func (r *OrganizationRepository) Create(ctx context.Context, org *entity.Organization) error {
	model := organizationToModel(org)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return apperror.Internal("create organization", err)
	}

	*org = *modelToOrganization(model)
	return nil
}

func (r *OrganizationRepository) FindByID(ctx context.Context, id uuid.UUID) (*entity.Organization, error) {
	var model OrganizationModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("organization not found")
		}
		return nil, apperror.Internal("find organization by id", err)
	}
	return modelToOrganization(&model), nil
}

func (r *OrganizationRepository) FindBySlug(ctx context.Context, slug string) (*entity.Organization, error) {
	var model OrganizationModel
	if err := r.db.WithContext(ctx).First(&model, "slug = ?", slug).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("organization not found")
		}
		return nil, apperror.Internal("find organization by slug", err)
	}
	return modelToOrganization(&model), nil
}

func (r *OrganizationRepository) Update(ctx context.Context, org *entity.Organization) error {
	model := organizationToModel(org)
	res := r.db.WithContext(ctx).Model(&OrganizationModel{}).Where("id = ?", org.ID).Updates(model)
	if res.Error != nil {
		return apperror.Internal("update organization", res.Error)
	}
	if res.RowsAffected == 0 {
		return apperror.NotFound("organization not found")
	}
	return nil
}

func organizationToModel(o *entity.Organization) *OrganizationModel {
	return &OrganizationModel{
		ID:        o.ID,
		Name:      o.Name,
		Slug:      o.Slug,
		Status:    string(o.Status),
		Timezone:  o.Timezone,
		CreatedBy: o.CreatedBy,
		UpdatedBy: o.UpdatedBy,
	}
}

func modelToOrganization(m *OrganizationModel) *entity.Organization {
	e := &entity.Organization{
		ID:        m.ID,
		Name:      m.Name,
		Slug:      m.Slug,
		Status:    entity.OrganizationStatus(m.Status),
		Timezone:  m.Timezone,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
		CreatedBy: m.CreatedBy,
		UpdatedBy: m.UpdatedBy,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
