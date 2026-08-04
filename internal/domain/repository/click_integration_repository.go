package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type ClickIntegrationRepository interface {
	// Upsert creates the org's Click integration row, or replaces the
	// existing one if already connected (reconnecting with new
	// merchant_id/service_id — e.g. after a Click account change — should
	// not require disconnecting first). Exactly one non-deleted row per
	// organization_id.
	Upsert(ctx context.Context, integration *entity.ClickIntegration) error
	FindByOrganization(ctx context.Context, orgID uuid.UUID) (*entity.ClickIntegration, error)
	Delete(ctx context.Context, orgID uuid.UUID) error
}
