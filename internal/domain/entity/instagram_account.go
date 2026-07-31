package entity

import (
	"time"

	"github.com/google/uuid"
)

type InstagramAccountStatus string

const (
	InstagramAccountStatusConnected InstagramAccountStatus = "connected"
	InstagramAccountStatusExpired   InstagramAccountStatus = "expired"
	InstagramAccountStatusRevoked   InstagramAccountStatus = "revoked"
	InstagramAccountStatusError     InstagramAccountStatus = "error"
)

// InstagramAccount represents one connected Instagram Professional account.
// AccessTokenEncrypted is ciphertext (AES-256-GCM, see pkg/crypto) — it must
// never be logged, never serialized into an API response, and only ever
// decrypted inside the usecase that needs to call the Graph API with it.
type InstagramAccount struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	IGUserID             string
	Username             *string
	AccessTokenEncrypted []byte
	TokenExpiresAt       *time.Time
	Status               InstagramAccountStatus
	WebhookSubscribed    bool
	ConnectedByUserID    *uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            *time.Time
}
