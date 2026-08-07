package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type CommentAutomationRepository struct {
	db *gorm.DB
}

func NewCommentAutomationRepository(db *gorm.DB) *CommentAutomationRepository {
	return &CommentAutomationRepository{db: db}
}

func (r *CommentAutomationRepository) Get(ctx context.Context, orgID uuid.UUID) (*entity.CommentAutomationSettings, error) {
	var model CommentAutomationSettingsModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("comment automation not configured")
		}
		return nil, apperror.Internal("find comment automation settings", err)
	}
	return modelToCommentAutomationSettings(&model), nil
}

// Upsert overwrites the org's single settings row — plain INSERT ... ON
// CONFLICT, same style as AIInsightsRepository.Upsert and
// PlatformSettingsRepository.Set.
func (r *CommentAutomationRepository) Upsert(ctx context.Context, settings *entity.CommentAutomationSettings) error {
	err := withTenant(ctx, r.db, settings.OrganizationID, func(tx *gorm.DB) error {
		return tx.Exec(
			`INSERT INTO comment_automation_settings (organization_id, enabled, public_reply_text)
			 VALUES (?, ?, ?)
			 ON CONFLICT (organization_id) DO UPDATE SET
			   enabled = EXCLUDED.enabled,
			   public_reply_text = EXCLUDED.public_reply_text`,
			settings.OrganizationID, settings.Enabled, settings.PublicReplyText,
		).Error
	})
	if err != nil {
		return apperror.Internal("upsert comment automation settings", err)
	}
	return nil
}

// ClaimComment returns (false, nil) on a duplicate-key violation rather
// than an error — that's the expected outcome for a redelivered webhook,
// not a failure. Matched on the error string rather than a driver-specific
// error code so this stays independent of which Postgres driver GORM is
// configured with; the unique index name is stable (migration 000017) and
// specific enough that no unrelated error can match it.
func (r *CommentAutomationRepository) ClaimComment(ctx context.Context, claim *entity.ProcessedComment) (bool, error) {
	model := &ProcessedCommentModel{
		ID:             claim.ID,
		OrganizationID: claim.OrganizationID,
		IGCommentID:    claim.IGCommentID,
	}
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, claim.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		if isDuplicateProcessedComment(err) {
			return false, nil
		}
		return false, apperror.Internal("claim processed comment", err)
	}

	claim.ID = model.ID
	return true, nil
}

func (r *CommentAutomationRepository) ReleaseComment(ctx context.Context, orgID uuid.UUID, igCommentID string) error {
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ? AND ig_comment_id = ?", orgID, igCommentID).
			Delete(&ProcessedCommentModel{}).Error
	})
	if err != nil {
		return apperror.Internal("release processed comment", err)
	}
	return nil
}

func isDuplicateProcessedComment(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "uq_processed_comments_comment_id") ||
		(strings.Contains(msg, "duplicate key") && strings.Contains(msg, "processed_comments"))
}

func modelToCommentAutomationSettings(m *CommentAutomationSettingsModel) *entity.CommentAutomationSettings {
	return &entity.CommentAutomationSettings{
		OrganizationID:  m.OrganizationID,
		Enabled:         m.Enabled,
		PublicReplyText: m.PublicReplyText,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}
