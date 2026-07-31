package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type WebhookLogRepository struct {
	db *gorm.DB
}

func NewWebhookLogRepository(db *gorm.DB) *WebhookLogRepository {
	return &WebhookLogRepository{db: db}
}

func (r *WebhookLogRepository) Create(ctx context.Context, log *entity.WebhookLog) error {
	model := webhookLogToModel(log)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}
	if model.ReceivedAt.IsZero() {
		model.ReceivedAt = time.Now()
	}

	if err := r.db.WithContext(ctx).Create(model).Error; err != nil {
		return apperror.Internal("create webhook log", err)
	}

	*log = *modelToWebhookLog(model)
	return nil
}

func (r *WebhookLogRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entity.WebhookStatus, errMsg *string) error {
	now := time.Now()
	res := r.db.WithContext(ctx).Model(&WebhookLogModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":        string(status),
			"error_message": errMsg,
			"processed_at":  now,
		})
	if res.Error != nil {
		return apperror.Internal("update webhook log status", res.Error)
	}
	if res.RowsAffected == 0 {
		return apperror.NotFound("webhook log not found")
	}
	return nil
}

func webhookLogToModel(w *entity.WebhookLog) *WebhookLogModel {
	return &WebhookLogModel{
		ID:             w.ID,
		OrganizationID: w.OrganizationID,
		Source:         string(w.Source),
		EventType:      w.EventType,
		Payload:        w.Payload,
		SignatureValid: w.SignatureValid,
		Status:         string(w.Status),
		ErrorMessage:   w.ErrorMessage,
		ReceivedAt:     w.ReceivedAt,
		ProcessedAt:    w.ProcessedAt,
	}
}

func modelToWebhookLog(m *WebhookLogModel) *entity.WebhookLog {
	return &entity.WebhookLog{
		ID:             m.ID,
		OrganizationID: m.OrganizationID,
		Source:         entity.WebhookSource(m.Source),
		EventType:      m.EventType,
		Payload:        m.Payload,
		SignatureValid: m.SignatureValid,
		Status:         entity.WebhookStatus(m.Status),
		ErrorMessage:   m.ErrorMessage,
		ReceivedAt:     m.ReceivedAt,
		ProcessedAt:    m.ProcessedAt,
	}
}
