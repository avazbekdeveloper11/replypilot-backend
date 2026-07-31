package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/repository"
)

// PlatformSettingModel intentionally has no DeletedAt — platform_settings
// rows are upserted in place (Set overwrites), never soft-deleted; there
// is no "disconnect" analog for a platform-wide secret, only "replace it".
type PlatformSettingModel struct {
	Key            string     `gorm:"column:key;primaryKey"`
	ValueEncrypted []byte     `gorm:"column:value_encrypted"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
	UpdatedBy      *uuid.UUID `gorm:"column:updated_by;type:uuid"`
}

func (PlatformSettingModel) TableName() string { return "platform_settings" }

type PlatformSettingsRepository struct {
	db *gorm.DB
}

func NewPlatformSettingsRepository(db *gorm.DB) *PlatformSettingsRepository {
	return &PlatformSettingsRepository{db: db}
}

// Get deliberately does NOT go through withTenant — platform_settings has
// no organization_id and no RLS (see migration 000010's doc comment,
// the same "global, not tenant" shape as PlanModel). This is a plain
// query, not a permissive-policy GUC bypass like the webhook/token-refresh
// lookups elsewhere in this package.
func (r *PlatformSettingsRepository) Get(ctx context.Context, key string) (*repository.PlatformSetting, bool, error) {
	var model PlatformSettingModel
	err := r.db.WithContext(ctx).First(&model, "key = ?", key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, apperror.Internal("get platform setting", err)
	}
	return &repository.PlatformSetting{
		Key:            model.Key,
		ValueEncrypted: model.ValueEncrypted,
		UpdatedAt:      model.UpdatedAt,
		UpdatedBy:      model.UpdatedBy,
	}, true, nil
}

// Set upserts via a plain INSERT ... ON CONFLICT — simpler and more
// explicit here than GORM's clause.OnConflict builder for a one-row,
// three-column table with no other write path in this codebase.
func (r *PlatformSettingsRepository) Set(ctx context.Context, key string, valueEncrypted []byte, updatedBy uuid.UUID) error {
	err := r.db.WithContext(ctx).Exec(
		`INSERT INTO platform_settings (key, value_encrypted, updated_at, updated_by)
		 VALUES (?, ?, now(), ?)
		 ON CONFLICT (key) DO UPDATE SET
		   value_encrypted = EXCLUDED.value_encrypted,
		   updated_at = EXCLUDED.updated_at,
		   updated_by = EXCLUDED.updated_by`,
		key, valueEncrypted, updatedBy,
	).Error
	if err != nil {
		return apperror.Internal("set platform setting", err)
	}
	return nil
}
