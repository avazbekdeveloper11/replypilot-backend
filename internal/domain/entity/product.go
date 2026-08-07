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
//
// PriceCents is a pointer: nil means "price on request" — a merchant listed
// the product without a fixed price (custom/negotiated pricing, made-to-
// order items, etc.). ai.UseCase.buildProductContext treats a nil price as
// "don't quote a number, ask for the customer's phone number instead" (see
// its doc comment) and never builds a Click payment link for that product —
// there's no amount to charge. Every other price_cents consumer
// (payment.WebhookUseCase, campaign.UseCase) must nil-check before
// dereferencing; a priced-but-zero product (PriceCents pointing at 0) is a
// different, valid state from "no price set" and is NOT treated as
// price-on-request.
type Product struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	Description    *string
	PriceCents     *int64
	Currency       string
	IsActive       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}
