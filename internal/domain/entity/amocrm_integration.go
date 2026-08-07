package entity

import (
	"time"

	"github.com/google/uuid"
)

type AmoCRMIntegrationStatus string

const (
	AmoCRMIntegrationStatusConnected AmoCRMIntegrationStatus = "connected"
	// AmoCRMIntegrationStatusExpired means amoCRM's refresh token itself
	// expired (unused for 3 months — see
	// https://developers.kommo.com/docs/oauth-20#what-is-a-refresh-token)
	// or was rejected as invalid. The org must reconnect via OAuth from
	// scratch; there is nothing left to refresh.
	AmoCRMIntegrationStatusExpired AmoCRMIntegrationStatus = "expired"
	// AmoCRMIntegrationStatusRevoked means the org's amoCRM administrator
	// uninstalled the integration from their amoCRM account's Settings ->
	// Integrations screen.
	AmoCRMIntegrationStatusRevoked AmoCRMIntegrationStatus = "revoked"
	AmoCRMIntegrationStatusError   AmoCRMIntegrationStatus = "error"
)

// AmoCRMIntegration is one organization's OAuth connection to its own
// amoCRM account. At most one per org (soft-deleted, not hard-deleted,
// on disconnect — same audit-trail reasoning as ClickIntegration).
//
// Unlike ClickIntegration, this DOES carry secrets: AccessTokenEncrypted
// (valid ~24h) and RefreshTokenEncrypted (valid ~3 months, and amoCRM
// issues a brand-new refresh token on every use — the old one keeps
// working until the new one is used once, per amoCRM's OAuth docs, but
// this codebase always persists the newest pair immediately after a
// refresh so there is only ever one refresh token "in flight" per org).
// Both are AES-256-GCM encrypted at rest with pkg/crypto, the same
// envelope encryption already used for Instagram access tokens — no new
// key.
type AmoCRMIntegration struct {
	ID                     uuid.UUID
	OrganizationID         uuid.UUID
	Subdomain              string
	AccessTokenEncrypted   []byte
	RefreshTokenEncrypted  []byte
	AccessTokenExpiresAt   time.Time
	Status                 AmoCRMIntegrationStatus
	ConnectedByUserID      *uuid.UUID
	CreatedAt              time.Time
	UpdatedAt              time.Time
	DeletedAt              *time.Time
}

// AmoCRMContactLink is the external-id mapping between one of this org's
// conversations and the amoCRM contact created for it — amoCRM's API has
// no "upsert a contact by an external id" method, so this table stands
// in for one, keeping repeat syncs idempotent (update in place) instead
// of creating a duplicate contact every time. See migration 000019's
// doc comment.
type AmoCRMContactLink struct {
	OrganizationID  uuid.UUID
	ConversationID  uuid.UUID
	AmoCRMContactID int64
	SyncedAt        time.Time
}
