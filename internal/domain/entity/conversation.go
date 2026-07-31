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

// Conversation is one DM thread between a connected InstagramAccount and a
// single customer. The status field drives the handoff state machine
// described in docs/ARCHITECTURE.md §5: AI_ACTIVE -> PENDING_HUMAN ->
// HUMAN_ACTIVE -> RESOLVED. Once a human sends a message the AI does not
// re-engage automatically on the same thread.
type Conversation struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	InstagramAccountID uuid.UUID
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
