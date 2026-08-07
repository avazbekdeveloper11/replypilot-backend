package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

// CustomerListParams drives CustomerRepository.ListSummaries. Deliberately
// a plain Limit, not cursor pagination like ConversationListParams — the
// customer database is sorted by total spend (a value that changes as new
// orders come in), which makes keyset pagination on it unstable across
// pages the way it's stable on an append-only "newest first" ordering.
// Fine for an MVP-sized shop's customer count; revisit if an org's
// customer list ever needs real pagination.
type CustomerListParams struct {
	OrganizationID uuid.UUID
	// Search, when non-empty, filters to conversations whose
	// customer_username case-insensitively contains this substring — same
	// convention as ConversationListParams.Search.
	Search string
	Limit  int
}

type CustomerRepository interface {
	// ListSummaries returns every conversation in the org annotated with
	// its paid-order totals, highest total spend first (ties broken by
	// most recently active) — the customer database's main list. See
	// entity.CustomerSummary's doc comment on why this is computed fresh,
	// not read from a stored table.
	ListSummaries(ctx context.Context, params CustomerListParams) ([]*entity.CustomerSummary, error)
}
