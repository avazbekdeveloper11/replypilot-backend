package entity

import (
	"time"

	"github.com/google/uuid"
)

// Product is one sellable item in an organization's own price catalog —
// distinct from KnowledgeDocument (unstructured RAG source text). The AI
// reply pipeline (internal/usecase/ai) reads active products directly
// (structured, not via embedding retrieval) so it can quote an exact price
// and, if internal/usecase/click's integration is connected for the org,
// generate a real Click payment link for that exact amount — see
// ai.UseCase's buildProductContext doc comment.
//
// PriceCents mirrors entity.Plan.PriceMonthlyCents's convention: an integer
// in the currency's smallest unit (tiyin, for UZS), never a float, so
// nothing here is ever subject to floating-point rounding. Click's payment
// link wants a decimal "N.NN" amount — that conversion happens once, at the
// point a link is actually built (see metaapi.BuildClickPaymentLink), not
// stored anywhere.
type Product struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	Description    *string
	PriceCents     int64
	Currency       string
	IsActive       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}
