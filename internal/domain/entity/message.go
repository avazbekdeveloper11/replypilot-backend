package entity

import (
	"time"

	"github.com/google/uuid"
)

type MessageDirection string

const (
	MessageDirectionInbound  MessageDirection = "inbound"
	MessageDirectionOutbound MessageDirection = "outbound"
)

type MessageSenderType string

const (
	MessageSenderCustomer MessageSenderType = "customer"
	MessageSenderAI       MessageSenderType = "ai"
	MessageSenderHuman    MessageSenderType = "human"
	MessageSenderSystem   MessageSenderType = "system"
)

type MessageType string

const (
	MessageTypeText         MessageType = "text"
	MessageTypeImage        MessageType = "image"
	MessageTypeVideo        MessageType = "video"
	MessageTypeAudio        MessageType = "audio"
	MessageTypeFile         MessageType = "file"
	MessageTypeQuickReply   MessageType = "quick_reply"
	MessageTypeStoryReply   MessageType = "story_reply"
	MessageTypeStoryMention MessageType = "story_mention"
	MessageTypeUnsupported  MessageType = "unsupported"
)

// Message is one turn in a Conversation. The backing table is partitioned
// monthly by CreatedAt (see database/schema.sql §10) — high volume, cheap
// retention via partition drop instead of row-by-row deletes.
type Message struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	ConversationID uuid.UUID
	Direction      MessageDirection
	SenderType     MessageSenderType
	SenderUserID   *uuid.UUID // set only when SenderType == human
	MessageType    MessageType
	Content        *string
	AttachmentURL  *string
	IGMessageID    *string // Meta's message id; used for idempotent ingestion
	Metadata       map[string]any
	CreatedAt      time.Time
	DeletedAt      *time.Time // moderation/removal only, not a normal edit path
}
