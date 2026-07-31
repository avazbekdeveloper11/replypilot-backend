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
}
