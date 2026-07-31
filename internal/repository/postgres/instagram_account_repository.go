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

type InstagramAccountRepository struct {
	db *gorm.DB
}

func NewInstagramAccountRepository(db *gorm.DB) *InstagramAccountRepository {
	return &InstagramAccountRepository{db: db}
}

func (r *InstagramAccountRepository) Create(ctx context.Context, account *entity.InstagramAccount) error {
	model := instagramAccountToModel(account)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, account.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create instagram account", err)
	}

	*account = *modelToInstagramAccount(model)
	return nil
}

func (r *InstagramAccountRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.InstagramAccount, error) {
	var model InstagramAccountModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id = ? AND organization_id = ?", id, orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("instagram account not found")
		}
		return nil, apperror.Internal("find instagram account by id", err)
	}
	return modelToInstagramAccount(&model), nil
}

// FindByIGUserID is the one query in this repository that deliberately does
// NOT go through withTenant: it exists precisely to answer "which
// organization owns this Instagram account", called from the webhook
// ingestion path before any tenant context is known — there is no org_id to
// scope by yet, that's what this call produces.
//
// Under the standard tenant_isolation policy alone this query would return
// zero rows (no app.current_org_id set). Migration 000003 adds a permissive
// SELECT-only policy (webhook_account_lookup) that allows the read when the
// session GUC app.webhook_lookup is 'on'. This method opts in by setting
// that GUC with SET LOCAL inside a transaction, so the elevated read is
// scoped to exactly this one query and auto-clears on commit — it cannot
// leak to any other query sharing the pooled connection. See migration
// 000003 for the full rationale.
func (r *InstagramAccountRepository) FindByIGUserID(ctx context.Context, igUserID string) (*entity.InstagramAccount, error) {
	var model InstagramAccountModel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL app.webhook_lookup = 'on'").Error; err != nil {
			return err
		}
		return tx.First(&model, "ig_user_id = ?", igUserID).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("instagram account not found")
		}
		return nil, apperror.Internal("find instagram account by ig user id", err)
	}
	return modelToInstagramAccount(&model), nil
}

func (r *InstagramAccountRepository) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.InstagramAccount, error) {
	var models []InstagramAccountModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list instagram accounts", err)
	}

	accounts := make([]*entity.InstagramAccount, 0, len(models))
	for i := range models {
		accounts = append(accounts, modelToInstagramAccount(&models[i]))
	}
	return accounts, nil
}

// ListNearingExpiry is cmd/token-refresh's one query, and the second
// deliberately-cross-tenant read on this table (the first is
// FindByIGUserID, for the webhook receiver). A scheduled job has no single
// org_id to scope by — it needs every organization's accounts nearing
// expiry in one pass. Under the standard tenant_isolation policy alone this
// would return zero rows; migration 000009 adds a permissive SELECT-only
// policy (token_refresh_lookup) gated on the session GUC
// app.token_refresh_lookup, set here with SET LOCAL inside a transaction
// exactly like FindByIGUserID's webhook_lookup — scoped to this one query,
// auto-clears on commit, cannot leak to another query on the pooled
// connection. See migration 000009 for the full rationale.
//
// Only status=connected accounts are considered: an account already
// flagged expired/revoked (see internal/usecase/ai.handleSendFailure) needs
// the user to reconnect via OAuth, not a token refresh — refreshing a token
// Meta already invalidated would just fail the same way SendMessage did.
func (r *InstagramAccountRepository) ListNearingExpiry(ctx context.Context, within time.Duration) ([]*entity.InstagramAccount, error) {
	var models []InstagramAccountModel
	cutoff := time.Now().Add(within)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL app.token_refresh_lookup = 'on'").Error; err != nil {
			return err
		}
		return tx.Where(
			"status = ? AND token_expires_at IS NOT NULL AND token_expires_at <= ?",
			string(entity.InstagramAccountStatusConnected), cutoff,
		).Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list instagram accounts nearing expiry", err)
	}

	accounts := make([]*entity.InstagramAccount, 0, len(models))
	for i := range models {
		accounts = append(accounts, modelToInstagramAccount(&models[i]))
	}
	return accounts, nil
}

func (r *InstagramAccountRepository) Update(ctx context.Context, account *entity.InstagramAccount) error {
	model := instagramAccountToModel(account)
	var rowsAffected int64
	err := withTenant(ctx, r.db, account.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&InstagramAccountModel{}).Where("id = ?", account.ID).Updates(model)
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update instagram account", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("instagram account not found")
	}
	return nil
}

// Delete soft-deletes (GORM's convention for a model with a DeletedAt
// field — see model.go) rather than hard-deleting. A hard delete would
// lose the audit trail of "this org used to have this account connected"
// and would orphan any ai_responses/conversations rows still referencing
// it; a disconnected account simply falls out of ListByOrganization
// (which, like every tenant-scoped query, implicitly excludes
// soft-deleted rows via GORM's default scope).
func (r *InstagramAccountRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND organization_id = ?", id, orgID).Delete(&InstagramAccountModel{})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("delete instagram account", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("instagram account not found")
	}
	return nil
}

func instagramAccountToModel(a *entity.InstagramAccount) *InstagramAccountModel {
	return &InstagramAccountModel{
		ID:                   a.ID,
		OrganizationID:       a.OrganizationID,
		IGUserID:             a.IGUserID,
		Username:             a.Username,
		AccessTokenEncrypted: a.AccessTokenEncrypted,
		TokenExpiresAt:       a.TokenExpiresAt,
		Status:               string(a.Status),
		WebhookSubscribed:    a.WebhookSubscribed,
		ConnectedByUserID:    a.ConnectedByUserID,
	}
}

func modelToInstagramAccount(m *InstagramAccountModel) *entity.InstagramAccount {
	e := &entity.InstagramAccount{
		ID:                   m.ID,
		OrganizationID:       m.OrganizationID,
		IGUserID:             m.IGUserID,
		Username:             m.Username,
		AccessTokenEncrypted: m.AccessTokenEncrypted,
		TokenExpiresAt:       m.TokenExpiresAt,
		Status:               entity.InstagramAccountStatus(m.Status),
		WebhookSubscribed:    m.WebhookSubscribed,
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
