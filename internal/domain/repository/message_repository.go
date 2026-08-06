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
}
