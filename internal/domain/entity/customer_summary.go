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

	// Segment/RecencyScore/FrequencyScore/MonetaryScore are computed by
	// usecase/customer's RFM scoring pass, not by the repository — see
	// that package's rfm.go doc comments. Scores are 1 (worst) to 5
	// (best), relative to this org's own customers; all four fields are
	// zero-valued (Segment == "") until that pass runs, so callers that
	// only need raw purchase totals (e.g. before segmentation existed)
	// still get a valid struct.
	Segment        RFMSegment
	RecencyScore   int
	FrequencyScore int
	MonetaryScore  int
}
