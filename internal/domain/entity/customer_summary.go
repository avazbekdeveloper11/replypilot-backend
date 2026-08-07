package entity

import (
	"time"

	"github.com/google/uuid"
)

// CustomerSummary is one row in the customer database (see
// internal/usecase/customer's package doc comment) — a conversation
// (this codebase's unit of customer identity, see Conversation's doc
// comment) annotated with its purchase history aggregated from orders.
// Deliberately a read-model, not a stored table: every field here is
// computed fresh from conversations/orders/messages, nothing is cached or
// kept in sync, so this can never drift from the real data the way a
// denormalized "customers" table with its own copy of these numbers
// could.
type CustomerSummary struct {
	ConversationID   uuid.UUID
	Channel          ConversationChannel
	CustomerUsername *string
	LastMessageAt    *time.Time
	// TotalPaidCents/PaidOrderCount only count orders with status=paid —
	// what this customer actually bought, not what they attempted. See
	// OrderRepository.ListByConversation's doc comment for the drill-down
	// view that DOES include every status.
	TotalPaidCents int64
	PaidOrderCount int
	LastPaidAt     *time.Time
}
