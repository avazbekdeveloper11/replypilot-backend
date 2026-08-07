// Package customer is the "who are our customers and what have they
// bought" read-model behind the dashboard's customer database — built so
// an admin can decide who deserves a cashback/discount based on real
// purchase history, not guesswork. See entity.CustomerSummary's doc
// comment on why this is computed fresh on every call rather than kept in
// a stored, syncable table.
package customer

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type UseCase struct {
	customers repository.CustomerRepository
	orders    repository.OrderRepository
	convRepo  repository.ConversationRepository
}

func New(customers repository.CustomerRepository, orders repository.OrderRepository, convRepo repository.ConversationRepository) *UseCase {
	return &UseCase{customers: customers, orders: orders, convRepo: convRepo}
}

// List returns the org's customer database, biggest spenders first — see
// repository.CustomerRepository.ListSummaries' doc comment.
func (uc *UseCase) List(ctx context.Context, orgID uuid.UUID, search string) ([]*entity.CustomerSummary, error) {
	return uc.customers.ListSummaries(ctx, repository.CustomerListParams{
		OrganizationID: orgID,
		Search:         search,
	})
}

// Orders returns one customer's full order history (every status, not
// just paid — see OrderRepository.ListByConversation's doc comment) for
// the customer database's drill-down view. FindByID first is a deliberate
// second check on top of row-level security, the same reasoning as
// conversation.UseCase.ListMessages' identical guard: it turns a
// conversation id belonging to a different org into a clean 404 instead
// of relying solely on RLS silently returning no rows.
func (uc *UseCase) Orders(ctx context.Context, orgID, conversationID uuid.UUID) ([]*entity.Order, error) {
	if _, err := uc.convRepo.FindByID(ctx, orgID, conversationID); err != nil {
		return nil, err
	}
	return uc.orders.ListByConversation(ctx, orgID, conversationID)
}
