package entity

import (
	"time"

	"github.com/google/uuid"
)

type TelegramAccountStatus string

const (
	TelegramAccountStatusConnected TelegramAccountStatus = "connected"
	TelegramAccountStatusError     TelegramAccountStatus = "error"
)

// TelegramAccount represents one bot an organization has connected as a
// Telegram Business chatbot (see migration 000014's header comment for how
// that feature works). Unlike InstagramAccount there is no OAuth token
// lifecycle here — BotTokenEncrypted is a long-lived secret the org pastes
// in from @BotFather, not something that expires and needs refreshing.
//
// BusinessConnectionID is nil until the org actually finishes pairing the
// bot inside their own Telegram app (Settings -> Telegram Business ->
// Chatbots) — connecting the token alone (telegram.ConnectUseCase.Connect)
// only registers the webhook; Telegram delivers a business_connection
// update the moment pairing completes, and that's what fills this in (see
// telegram.WebhookUseCase). Sending is impossible without it.
type TelegramAccount struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	BotTokenEncrypted    []byte
	BotUsername          *string
	BusinessConnectionID *string
	Status               TelegramAccountStatus
	ConnectedByUserID    *uuid.UUID
	// NotifyChatID is the admin's own Telegram chat id, bound only via the
	// verification-code handshake described below — nil means "no admin has
	// verified yet," distinct from NotifyOnLead/NotifyOnPayment being false.
	// See telegram.NotifyUseCase, the only thing that reads it.
	NotifyChatID *int64
	// NotifyVerifyCode is a random one-time code shown in Settings
	// (telegram.ConnectUseCase.GenerateNotifyCode) that the admin sends as a
	// plain Telegram message to this same bot to prove they control that
	// chat — see telegram.WebhookUseCase's plain-message handling. Cleared
	// back to nil once it's matched, so it can't be reused or observed
	// later.
	NotifyVerifyCode *string
	// NotifyOnLead/NotifyOnPayment gate NotifyUseCase.NotifyLead/NotifyPayment
	// independently of NotifyChatID being set — both default true (migration
	// 000021) so a freshly-verified admin starts receiving both kinds of
	// notification without an extra opt-in step.
	NotifyOnLead    bool
	NotifyOnPayment bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
}
