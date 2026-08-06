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
}
