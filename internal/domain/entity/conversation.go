package entity

import (
	"time"

	"github.com/google/uuid"
)

type ConversationStatus string

const (
	ConversationStatusAIActive     ConversationStatus = "ai_active"
	ConversationStatusPendingHuman ConversationStatus = "pending_human"
	ConversationStatusHumanActive  ConversationStatus = "human_active"
	ConversationStatusResolved     ConversationStatus = "resolved"
	ConversationStatusClosed       ConversationStatus = "closed"
)

// ConversationChannel is which platform this thread lives on. Exactly one
// of InstagramAccountID/TelegramAccountID is set, matching which channel —
// enforced at the DB level by chk_conversations_channel_account (see
// migration 000014). Added alongside Telegram support; every conversation
// created before that migration is backfilled to ChannelInstagram.
type ConversationChannel string

const (
	ConversationChannelInstagram ConversationChannel = "instagram"
	ConversationChannelTelegram  ConversationChannel = "telegram"
)

// Conversation is one DM thread between a connected account (Instagram or,
// since migration 000014, Telegram) and a single customer. The status field
// drives the handoff state machine described in docs/ARCHITECTURE.md §5:
// AI_ACTIVE -> PENDING_HUMAN -> HUMAN_ACTIVE -> RESOLVED. Once a human sends
// a message the AI does not re-engage automatically on the same thread.
type Conversation struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Channel        ConversationChannel
	// InstagramAccountID is set only when Channel == ConversationChannelInstagram.
	InstagramAccountID uuid.UUID
	// TelegramAccountID is set only when Channel == ConversationChannelTelegram.
	TelegramAccountID *uuid.UUID
	// CustomerIGID is the customer's per-channel external id, despite the
	// name: Instagram's IGSID for an Instagram thread, or the customer's
	// Telegram chat_id (formatted as a plain decimal string) for a Telegram
	// thread. Not renamed to something channel-neutral — see migration
	// 000014's header comment on why the column (and this field) are reused
	// rather than duplicated per channel.
	CustomerIGID       string
	CustomerUsername   *string
	Status             ConversationStatus
	AssignedUserID     *uuid.UUID
	LastMessageAt      *time.Time
	LastMessagePreview *string
	UnreadCount        int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
}
