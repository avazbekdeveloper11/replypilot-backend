package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type OrderRepository interface {
	Create(ctx context.Context, order *entity.Order) error
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Order, error)
	// FindByTransactionParam is how payment.WebhookUseCase's Prepare step
	// finds an already-created order (Click retried a Prepare it never got
	// a response for, or the customer reopened the same payment link) —
	// see entity.Order's doc comment on why click_transaction_param is
	// deterministic per conversation+product rather than a fresh id.
	// Returns apperror.NotFound (not nil, nil) when no order exists yet —
	// Prepare treats that as "create one now", not an error condition.
	FindByTransactionParam(ctx context.Context, orgID uuid.UUID, transactionParam string) (*entity.Order, error)
	Update(ctx context.Context, order *entity.Order) error
	// ListByConversation backs the customer database's per-customer order
	// history drill-down (internal/usecase/customer) — every order for
	// this conversation, any status, newest first. Unlike Stats, this
	// deliberately doesn't filter to status='paid' only: the admin looking
	// at one customer's history benefits from seeing a pending/failed
	// attempt too (e.g. "they tried to buy this and it didn't go
	// through"), not just confirmed sales.
	ListByConversation(ctx context.Context, orgID, conversationID uuid.UUID) ([]*entity.Order, error)
	// Stats is a real aggregate query (COUNT/SUM over status='paid'), not
	// something Gemini computes — internal/usecase/insights only asks
	// Gemini to narrate this alongside a qualitative read of recent
	// messages. Every order in this codebase currently originates from an
	// AI-sent Click link (see internal/usecase/ai.buildProductContext), so
	// this IS "how many sales did we make through the AI" — not an
	// approximation of it.
	Stats(ctx context.Context, orgID uuid.UUID) (*OrderStats, error)
}

// OrderStats is Stats' result shape — mirrors repository.ConversationStats'
// role for the dashboard's existing aggregate queries.
type OrderStats struct {
	PaidCount       int
	PaidAmountCents int64
}
