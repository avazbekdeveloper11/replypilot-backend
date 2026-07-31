package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type WebhookLogRepository interface {
	Create(ctx context.Context, log *entity.WebhookLog) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status entity.WebhookStatus, errMsg *string) error
}
