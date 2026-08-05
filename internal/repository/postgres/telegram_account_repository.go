package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type TelegramAccountRepository struct {
	db *gorm.DB
}

func NewTelegramAccountRepository(db *gorm.DB) *TelegramAccountRepository {
	return &TelegramAccountRepository{db: db}
}

func (r *TelegramAccountRepository) Create(ctx context.Context, account *entity.TelegramAccount) error {
	model := telegramAccountToModel(account)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, account.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create telegram account", err)
	}

	*account = *modelToTelegramAccount(model)
	return nil
}

func (r *TelegramAccountRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TelegramAccount, error) {
	var model TelegramAccountModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id = ? AND organization_id = ?", id, orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("telegram account not found")
		}
		return nil, apperror.Internal("find telegram account by id", err)
	}
	return modelToTelegramAccount(&model), nil
}

// FindByIDForWebhook deliberately does NOT go through withTenant — see the
// interface doc comment and migration 000014's webhook_account_lookup
// policy, the same SET LOCAL app.webhook_lookup pattern
// InstagramAccountRepository.FindByIGUserID uses (see that method's doc
// comment for the full RLS rationale, not repeated here).
func (r *TelegramAccountRepository) FindByIDForWebhook(ctx context.Context, id uuid.UUID) (*entity.TelegramAccount, error) {
	var model TelegramAccountModel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL app.webhook_lookup = 'on'").Error; err != nil {
			return err
		}
		return tx.First(&model, "id = ?", id).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("telegram account not found")
		}
		return nil, apperror.Internal("find telegram account for webhook", err)
	}
	return modelToTelegramAccount(&model), nil
}

func (r *TelegramAccountRepository) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.TelegramAccount, error) {
	var models []TelegramAccountModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list telegram accounts", err)
	}

	accounts := make([]*entity.TelegramAccount, 0, len(models))
	for i := range models {
		accounts = append(accounts, modelToTelegramAccount(&models[i]))
	}
	return accounts, nil
}

// Update writes every field via an explicit map, not GORM's struct-based
// Updates — struct-based Updates silently skips zero-value fields
// (including a nil pointer), which would make it impossible to ever clear
// BusinessConnectionID back to NULL (telegram.ConnectUseCase.Connect does
// exactly that on reconnect with a different bot token — see its doc
// comment). Every call site sets every field on the passed entity to its
// intended final value before calling Update, so unconditionally including
// all of them here is correct, not just a workaround.
func (r *TelegramAccountRepository) Update(ctx context.Context, account *entity.TelegramAccount) error {
	model := telegramAccountToModel(account)
	var rowsAffected int64
	err := withTenant(ctx, r.db, account.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&TelegramAccountModel{}).Where("id = ?", account.ID).Updates(map[string]any{
			"bot_token_encrypted":    model.BotTokenEncrypted,
			"bot_username":           model.BotUsername,
			"business_connection_id": model.BusinessConnectionID,
			"status":                 model.Status,
			"connected_by_user_id":   model.ConnectedByUserID,
		})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update telegram account", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("telegram account not found")
	}
	return nil
}

// Delete soft-deletes — same reasoning as InstagramAccountRepository.Delete:
// preserves the audit trail and avoids orphaning conversations/messages
// still referencing this account.
func (r *TelegramAccountRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND organization_id = ?", id, orgID).Delete(&TelegramAccountModel{})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("delete telegram account", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("telegram account not found")
	}
	return nil
}

func telegramAccountToModel(a *entity.TelegramAccount) *TelegramAccountModel {
	return &TelegramAccountModel{
		ID:                   a.ID,
		OrganizationID:       a.OrganizationID,
		BotTokenEncrypted:    a.BotTokenEncrypted,
		BotUsername:          a.BotUsername,
		BusinessConnectionID: a.BusinessConnectionID,
		Status:               string(a.Status),
		ConnectedByUserID:    a.ConnectedByUserID,
	}
}

func modelToTelegramAccount(m *TelegramAccountModel) *entity.TelegramAccount {
	e := &entity.TelegramAccount{
		ID:                   m.ID,
		OrganizationID:       m.OrganizationID,
		BotTokenEncrypted:    m.BotTokenEncrypted,
		BotUsername:          m.BotUsername,
		BusinessConnectionID: m.BusinessConnectionID,
		Status:               entity.TelegramAccountStatus(m.Status),
		ConnectedByUserID:    m.ConnectedByUserID,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
