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
	// Search, when non-empty, filters to conversations whose
	// customer_username case-insensitively contains this substring. It's
	// deliberately not full-text search — customer_username is a short
	// handle, not prose, so a simple ILIKE is the right tool.
	Search       string
	CursorBefore *time.Time
	Limit        int
}

// BroadcastCandidate is one conversation resolved as a possible recipient
// for campaign.UseCase.Draft/Send — see that package's doc comment.
// LastCustomerMessageAt is deliberately NOT the same as
// entity.Conversation.LastMessageAt (which bumps on outbound sends too):
// it's the timestamp of the customer's own most recent inbound message,
// the one number that decides both which "how long have they gone quiet"
// segment a conversation falls into AND, for Instagram, whether Meta's
// 24-hour messaging window is still open. HasPaidOrder lets a segment
// exclude customers who already bought (an "exclude_if_paid" instruction
// like "sotib olmagan mijozlarga" — customers who haven't purchased).
type BroadcastCandidate struct {
	Conversation          *entity.Conversation
	LastCustomerMessageAt time.Time
	HasPaidOrder          bool
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
	// FindByTelegramAccountAndCustomer is FindByAccountAndCustomer's
	// Telegram-channel counterpart — kept as its own method (rather than
	// overloading FindByAccountAndCustomer's instagramAccountID param) so
	// the postgres implementation can filter on channel = 'telegram'
	// explicitly instead of relying on callers never mixing up which kind
	// of account id they're passing.
	FindByTelegramAccountAndCustomer(ctx context.Context, orgID, telegramAccountID uuid.UUID, customerChatID string) (*entity.Conversation, error)
	List(ctx context.Context, params ConversationListParams) ([]*entity.Conversation, error)
	Update(ctx context.Context, conv *entity.Conversation) error
	// ListBroadcastCandidates backs campaign.UseCase.Draft's segment
	// matching — conversations whose customer last messaged between
	// minDaysAgo and maxDaysAgo (inclusive; nil maxDaysAgo means no upper
	// bound — "N or more days ago") ago. channel, when non-nil, restricts
	// to that one channel. Ordered newest-quiet-first (closest to
	// minDaysAgo) so a capped result set favors the most plausibly
	// re-engageable customers over ones gone quiet for months.
	ListBroadcastCandidates(ctx context.Context, orgID uuid.UUID, minDaysAgo int, maxDaysAgo *int, channel *entity.ConversationChannel, limit int) ([]*BroadcastCandidate, error)
}
