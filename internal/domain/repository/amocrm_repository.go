package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

// AmoCRMRepository is the persistence port for both amoCRM tables —
// combined into one interface (rather than one per table, like most of
// this codebase's repositories) because every caller that needs one
// needs the other: OAuthUseCase never touches links, but SyncUseCase
// needs both the org's connection AND the link for the specific
// customer being synced, on every call.
type AmoCRMRepository interface {
	// Upsert replaces the org's existing connection in place if one
	// exists (reconnecting after a revoke, or a fresh OAuth grant),
	// otherwise creates the first one — same pattern as
	// ClickIntegrationRepository.Upsert.
	Upsert(ctx context.Context, integration *entity.AmoCRMIntegration) error
	FindByOrganization(ctx context.Context, orgID uuid.UUID) (*entity.AmoCRMIntegration, error)
	// Update persists a mutated integration in place — used after a
	// token refresh (new access+refresh token pair, new expiry) and
	// after flipping Status on an auth failure. Deliberately separate
	// from Upsert: Update expects the row to already exist and errors
	// (apperror.NotFound) if it doesn't, rather than silently creating
	// one out of a partially-filled struct.
	Update(ctx context.Context, integration *entity.AmoCRMIntegration) error
	Delete(ctx context.Context, orgID uuid.UUID) error

	// FindContactLink returns (nil, nil) — not a NotFound error — when
	// this conversation has never been synced, same "absence is a valid
	// state, not a caller error" convention as click.UseCase.Get.
	FindContactLink(ctx context.Context, orgID, conversationID uuid.UUID) (*entity.AmoCRMContactLink, error)
	// UpsertContactLink records/updates which amoCRM contact id a
	// conversation maps to.
	UpsertContactLink(ctx context.Context, link *entity.AmoCRMContactLink) error
}
