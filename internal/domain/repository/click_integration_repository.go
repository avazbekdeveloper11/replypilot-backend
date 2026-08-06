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
	// FindByServiceIDForWebhook deliberately does NOT go through the normal
	// tenant-scoped query path — see TelegramAccountRepository.FindByIDForWebhook's
	// doc comment for the identical SET LOCAL app.webhook_lookup rationale.
	// Click's Prepare/Complete callback has no org context at all until this
	// call resolves one from the service_id in the payload; every other
	// order/payment lookup after that point uses the normal tenant-scoped
	// path once the org is known.
	FindByServiceIDForWebhook(ctx context.Context, serviceID string) (*entity.ClickIntegration, error)
	Delete(ctx context.Context, orgID uuid.UUID) error
}
