package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type MessageListParams struct {
	OrganizationID uuid.UUID
	ConversationID uuid.UUID
	CursorBefore   *time.Time
	Limit          int
}

type MessageRepository interface {
	Create(ctx context.Context, msg *entity.Message) error
	// FindByIGMessageID backs idempotent webhook ingestion — Meta may
	// redeliver the same event; this is the DB-level check behind the
	// Redis idempotency key described in docs/ARCHITECTURE.md §6. orgID is
	// required (not derived) for the same row-level-security reason as
	// ConversationRepository.FindByAccountAndCustomer above.
	FindByIGMessageID(ctx context.Context, orgID uuid.UUID, igMessageID string) (*entity.Message, error)
	List(ctx context.Context, params MessageListParams) ([]*entity.Message, error)
	// ListRecentInboundByOrganization feeds internal/usecase/insights'
	// org-wide sentiment/theme synthesis — real customer text (direction
	// inbound, sender_type customer) only, newest first, capped at limit.
	// Not scoped to one conversation (unlike List), and deliberately not
	// filtered any further (e.g. by having text content) here — that
	// filtering happens in the usecase, which also decides how much of
	// each message's text actually reaches the Gemini prompt.
	ListRecentInboundByOrganization(ctx context.Context, orgID uuid.UUID, limit int) ([]*entity.Message, error)
	// LastCustomerMessageAt returns the timestamp of this conversation's
	// most recent inbound, customer-sent message — nil if the customer has
	// never sent one (shouldn't normally happen; every conversation is
	// created by an inbound message, but defensive nonetheless). This is
	// the single number Instagram's 24-hour messaging-window rule is based
	// on, distinct from conversations.LastMessageAt which also bumps on
	// outbound sends — see campaign.UseCase.Send's doc comment for why a
	// point-in-time re-check at send time needs this, not the cached
	// per-candidate value campaign.UseCase.Draft already computed via
	// ConversationRepository.ListBroadcastCandidates.
	LastCustomerMessageAt(ctx context.Context, orgID, conversationID uuid.UUID) (*time.Time, error)
}
