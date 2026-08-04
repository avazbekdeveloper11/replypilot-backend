package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type ClickIntegrationRepository struct {
	db *gorm.DB
}

func NewClickIntegrationRepository(db *gorm.DB) *ClickIntegrationRepository {
	return &ClickIntegrationRepository{db: db}
}

// Upsert: replace the org's existing connection in place if one exists
// (reconnecting with new credentials), otherwise create the first one. Runs
// inside one tenant-scoped transaction so a concurrent connect attempt from
// the same org can't race into two live rows despite the unique index
// covering only non-deleted rows.
func (r *ClickIntegrationRepository) Upsert(ctx context.Context, integration *entity.ClickIntegration) error {
	err := withTenant(ctx, r.db, integration.OrganizationID, func(tx *gorm.DB) error {
		var existing ClickIntegrationModel
		findErr := tx.Where("organization_id = ?", integration.OrganizationID).First(&existing).Error
		switch {
		case findErr == nil:
			existing.MerchantID = integration.MerchantID
			existing.ServiceID = integration.ServiceID
			existing.MerchantUserID = integration.MerchantUserID
			existing.ConnectedByUserID = integration.ConnectedByUserID
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			*integration = *modelToClickIntegration(&existing)
			return nil
		case errors.Is(findErr, gorm.ErrRecordNotFound):
			model := clickIntegrationToModel(integration)
			if model.ID == uuid.Nil {
				model.ID = uuid.New()
			}
			if err := tx.Create(model).Error; err != nil {
				return err
			}
			*integration = *modelToClickIntegration(model)
			return nil
		default:
			return findErr
		}
	})
	if err != nil {
		return apperror.Internal("upsert click integration", err)
	}
	return nil
}

func (r *ClickIntegrationRepository) FindByOrganization(ctx context.Context, orgID uuid.UUID) (*entity.ClickIntegration, error) {
	var model ClickIntegrationModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("click integration not connected")
		}
		return nil, apperror.Internal("find click integration", err)
	}
	return modelToClickIntegration(&model), nil
}

func (r *ClickIntegrationRepository) Delete(ctx context.Context, orgID uuid.UUID) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Where("organization_id = ?", orgID).Delete(&ClickIntegrationModel{})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("delete click integration", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("click integration not connected")
	}
	return nil
}

func clickIntegrationToModel(c *entity.ClickIntegration) *ClickIntegrationModel {
	return &ClickIntegrationModel{
		ID:                c.ID,
		OrganizationID:    c.OrganizationID,
		MerchantID:        c.MerchantID,
		ServiceID:         c.ServiceID,
		MerchantUserID:    c.MerchantUserID,
		ConnectedByUserID: c.ConnectedByUserID,
	}
}

func modelToClickIntegration(m *ClickIntegrationModel) *entity.ClickIntegration {
	e := &entity.ClickIntegration{
		ID:                m.ID,
		OrganizationID:    m.OrganizationID,
		MerchantID:        m.MerchantID,
		ServiceID:         m.ServiceID,
		MerchantUserID:    m.MerchantUserID,
		ConnectedByUserID: m.ConnectedByUserID,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
