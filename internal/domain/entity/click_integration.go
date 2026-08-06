package entity

import (
	"time"

	"github.com/google/uuid"
)

// ClickIntegration is one organization's connection to Click (click.uz), a
// Uzbek payment provider. Unlike InstagramAccount, there is no OAuth token
// here to encrypt or expire: MerchantID and ServiceID are Click's own
// public identifiers for the org's merchant/service registration — the
// same values a merchant would put in a plain HTML payment button on their
// own website (see docs.click.uz/en/click-button) — not secrets. Storing
// them in plaintext is correct, not a shortcut.
//
// SecretKeyEncrypted IS a secret, unlike the two fields above: it's what
// Click's merchant cabinet issues alongside merchant_id/service_id to sign
// every Prepare/Complete webhook callback (see internal/integration/clickapi's
// webhook signature verification and internal/usecase/payment.WebhookUseCase).
// Nil for any integration connected before this field existed, or for an org
// that hasn't pasted it in yet — the webhook simply can't verify (and
// therefore can't process) that org's payment confirmations until it's set,
// same "boots without it, feature errors until configured" posture as
// TelegramAccount.BotTokenEncrypted.
//
// At most one row per organization (soft-deleted, not hard-deleted, on
// disconnect — same audit-trail reasoning as everything else in this
// codebase that uses DeletedAt rather than a real DELETE). Presence of a
// non-deleted row IS "connected" — there's no separate status enum because
// there's no token to go stale; disconnecting is the only state transition.
type ClickIntegration struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	MerchantID         string
	ServiceID          string
	MerchantUserID     *string
	SecretKeyEncrypted []byte
	ConnectedByUserID  *uuid.UUID
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
}
