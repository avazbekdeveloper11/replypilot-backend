package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

// ConversationListParams drives cursor-based pagination: pass the
// LastMessageAt of the last row from the previous page as CursorBefore to
// get the next page. Offset pagination is deliberately not offered — it
// degrades badly once a tenant has tens of thousands of conversations.
type ConversationListParams struct {
	OrganizationID uuid.UUID
	Status         *entity.ConversationStatus
	CursorBefore   *time.Time
	Limit          int
}

type ConversationRepository interface {
	Create(ctx context.Context, conv *entity.Conversation) error
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Conversation, error)
	// FindByAccountAndCustomer takes orgID explicitly (rather than deriving
	// it) because it's called from the webhook ingestion path where the
	// caller has already resolved the owning organization via
	// InstagramAccountRepository — passing it through keeps this query
	// compliant with the row-level-security policy on conversations
	// instead of needing its own bypass.
	FindByAccountAndCustomer(ctx context.Context, orgID, instagramAccountID uuid.UUID, customerIGID string) (*entity.Conversation, error)
	List(ctx context.Context, params ConversationListParams) ([]*entity.Conversation, error)
	Update(ctx context.Context, conv *entity.Conversation) error
}
