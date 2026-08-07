package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type AmoCRMRepository struct {
	db *gorm.DB
}

func NewAmoCRMRepository(db *gorm.DB) *AmoCRMRepository {
	return &AmoCRMRepository{db: db}
}

// Upsert: same shape as ClickIntegrationRepository.Upsert — replace the
// org's existing connection in place if one exists, otherwise create the
// first one, inside one tenant-scoped transaction so a concurrent
// connect attempt can't race into two live rows.
func (r *AmoCRMRepository) Upsert(ctx context.Context, integration *entity.AmoCRMIntegration) error {
	err := withTenant(ctx, r.db, integration.OrganizationID, func(tx *gorm.DB) error {
		var existing AmoCRMIntegrationModel
		findErr := tx.Where("organization_id = ?", integration.OrganizationID).First(&existing).Error
		switch {
		case findErr == nil:
			existing.Subdomain = integration.Subdomain
			existing.AccessTokenEncrypted = integration.AccessTokenEncrypted
			existing.RefreshTokenEncrypted = integration.RefreshTokenEncrypted
			existing.AccessTokenExpiresAt = integration.AccessTokenExpiresAt
			existing.Status = string(integration.Status)
			if integration.ConnectedByUserID != nil {
				existing.ConnectedByUserID = integration.ConnectedByUserID
			}
			if err := tx.Save(&existing).Error; err != nil {
				return err
			}
			*integration = *modelToAmoCRMIntegration(&existing)
			return nil
		case errors.Is(findErr, gorm.ErrRecordNotFound):
			model := amoCRMIntegrationToModel(integration)
			if model.ID == uuid.Nil {
				model.ID = uuid.New()
			}
			if err := tx.Create(model).Error; err != nil {
				return err
			}
			*integration = *modelToAmoCRMIntegration(model)
			return nil
		default:
			return findErr
		}
	})
	if err != nil {
		return apperror.Internal("upsert amocrm integration", err)
	}
	return nil
}

func (r *AmoCRMRepository) FindByOrganization(ctx context.Context, orgID uuid.UUID) (*entity.AmoCRMIntegration, error) {
	var model AmoCRMIntegrationModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("amocrm integration not connected")
		}
		return nil, apperror.Internal("find amocrm integration", err)
	}
	return modelToAmoCRMIntegration(&model), nil
}

// Update writes back a mutated integration (token refresh, status
// change) via an explicit map — not a struct-based Updates — for the
// same reason organization.UpdateBusinessHours uses one: GORM's
// struct-Updates silently skips zero-value fields, and Status is a
// plain string that could legitimately need to be set alongside a
// refreshed token in the same call.
func (r *AmoCRMRepository) Update(ctx context.Context, integration *entity.AmoCRMIntegration) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, integration.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&AmoCRMIntegrationModel{}).Where("id = ?", integration.ID).Updates(map[string]any{
			"subdomain":                integration.Subdomain,
			"access_token_encrypted":   integration.AccessTokenEncrypted,
			"refresh_token_encrypted":  integration.RefreshTokenEncrypted,
			"access_token_expires_at":  integration.AccessTokenExpiresAt,
			"status":                   string(integration.Status),
		})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update amocrm integration", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("amocrm integration not found")
	}
	return nil
}

func (r *AmoCRMRepository) Delete(ctx context.Context, orgID uuid.UUID) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Where("organization_id = ?", orgID).Delete(&AmoCRMIntegrationModel{})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("delete amocrm integration", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("amocrm integration not connected")
	}
	return nil
}

func (r *AmoCRMRepository) FindContactLink(ctx context.Context, orgID, conversationID uuid.UUID) (*entity.AmoCRMContactLink, error) {
	var model AmoCRMContactLinkModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ? AND conversation_id = ?", orgID, conversationID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, apperror.Internal("find amocrm contact link", err)
	}
	return &entity.AmoCRMContactLink{
		OrganizationID:  model.OrganizationID,
		ConversationID:  model.ConversationID,
		AmoCRMContactID: model.AmoCRMContactID,
		SyncedAt:        model.SyncedAt,
	}, nil
}

// UpsertContactLink relies on the composite primary key
// (organization_id, conversation_id) — ON CONFLICT semantics via GORM's
// clause.OnConflict, same "insert or replace" behavior as the rest of
// this repository's Upsert, just expressed as a single SQL upsert since
// the primary key already enforces uniqueness (no find-then-branch
// needed here, unlike the org-level Upsert above where the unique
// constraint is a partial index GORM can't target directly).
func (r *AmoCRMRepository) UpsertContactLink(ctx context.Context, link *entity.AmoCRMContactLink) error {
	model := &AmoCRMContactLinkModel{
		OrganizationID:  link.OrganizationID,
		ConversationID:  link.ConversationID,
		AmoCRMContactID: link.AmoCRMContactID,
	}
	err := withTenant(ctx, r.db, link.OrganizationID, func(tx *gorm.DB) error {
		return tx.Exec(
			`INSERT INTO amocrm_contact_links (organization_id, conversation_id, amocrm_contact_id, synced_at)
			 VALUES (?, ?, ?, now())
			 ON CONFLICT (organization_id, conversation_id)
			 DO UPDATE SET amocrm_contact_id = EXCLUDED.amocrm_contact_id, synced_at = now()`,
			model.OrganizationID, model.ConversationID, model.AmoCRMContactID,
		).Error
	})
	if err != nil {
		return apperror.Internal("upsert amocrm contact link", err)
	}
	return nil
}

func amoCRMIntegrationToModel(i *entity.AmoCRMIntegration) *AmoCRMIntegrationModel {
	return &AmoCRMIntegrationModel{
		ID:                    i.ID,
		OrganizationID:        i.OrganizationID,
		Subdomain:             i.Subdomain,
		AccessTokenEncrypted:  i.AccessTokenEncrypted,
		RefreshTokenEncrypted: i.RefreshTokenEncrypted,
		AccessTokenExpiresAt:  i.AccessTokenExpiresAt,
		Status:                string(i.Status),
		ConnectedByUserID:     i.ConnectedByUserID,
	}
}

func modelToAmoCRMIntegration(m *AmoCRMIntegrationModel) *entity.AmoCRMIntegration {
	e := &entity.AmoCRMIntegration{
		ID:                    m.ID,
		OrganizationID:        m.OrganizationID,
		Subdomain:             m.Subdomain,
		AccessTokenEncrypted:  m.AccessTokenEncrypted,
		RefreshTokenEncrypted: m.RefreshTokenEncrypted,
		AccessTokenExpiresAt:  m.AccessTokenExpiresAt,
		Status:                entity.AmoCRMIntegrationStatus(m.Status),
		ConnectedByUserID:     m.ConnectedByUserID,
		CreatedAt:             m.CreatedAt,
		UpdatedAt:             m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
