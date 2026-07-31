package entity

import (
	"time"

	"github.com/google/uuid"
)

type WebhookSource string

const (
	WebhookSourceMeta     WebhookSource = "meta"
	WebhookSourceStripe   WebhookSource = "stripe"
	WebhookSourceTelegram WebhookSource = "telegram"
)

type WebhookStatus string

const (
	WebhookStatusReceived   WebhookStatus = "received"
	WebhookStatusProcessing WebhookStatus = "processing"
	WebhookStatusProcessed  WebhookStatus = "processed"
	WebhookStatusFailed     WebhookStatus = "failed"
	WebhookStatusIgnored    WebhookStatus = "ignored"
)

// WebhookLog records every inbound webhook delivery, valid or not — it is
// the audit trail for "did Meta actually send this" and the source of truth
// when a signature check fails and you need to know whether that's an
// attacker or a misconfigured app secret. OrganizationID is nullable: a
// webhook whose signature fails, or whose payload can't be resolved to a
// known Instagram account, still gets logged with no tenant attached.
type WebhookLog struct {
	ID             uuid.UUID
	OrganizationID *uuid.UUID
	Source         WebhookSource
	EventType      *string
	Payload        []byte
	SignatureValid bool
	Status         WebhookStatus
	ErrorMessage   *string
	ReceivedAt     time.Time
	ProcessedAt    *time.Time
}
