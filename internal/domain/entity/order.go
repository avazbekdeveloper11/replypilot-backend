package entity

import (
	"time"

	"github.com/google/uuid"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusPaid      OrderStatus = "paid"
	OrderStatusFailed    OrderStatus = "failed"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// Order is one customer's attempt to buy one Product through a Click
// payment link the AI sent in a Conversation. It does NOT get created when
// the link itself is built (see internal/usecase/ai's buildProductContext,
// which builds a link for every active product on every inbound message) —
// only payment.WebhookUseCase creates one, lazily, the first time Click's
// Prepare callback confirms a customer actually opened the checkout page.
// See migration 000015's table comment for the full reasoning.
//
// ProductID is nullable (ON DELETE SET NULL) because a product can be
// deleted after an order referencing it already exists — ProductName and
// AmountCents are snapshotted at creation time specifically so the order
// (and the admin/customer-facing messages built from it) stay meaningful
// even if that happens.
type Order struct {
	ID                    uuid.UUID
	OrganizationID        uuid.UUID
	ConversationID        uuid.UUID
	ProductID             *uuid.UUID
	ProductNameSnapshot   string
	AmountCents           int64
	Currency              string
	Status                OrderStatus
	ClickTransactionParam string
	ClickTransID          *int64
	PaidAt                *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}
