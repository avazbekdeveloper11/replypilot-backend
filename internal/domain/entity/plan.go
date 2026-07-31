package entity

import (
	"time"

	"github.com/google/uuid"
)

// Plan is a purchasable tier — seeded reference data (see
// migrations/000002), not something a tenant creates. StripePriceID*
// (migrations/000006) is nil for a plan with no Stripe-sold price (the
// 'enterprise' tier is sales-assisted, never self-serve Checkout).
type Plan struct {
	ID                   uuid.UUID
	Code                 string
	Name                 string
	PriceMonthlyCents    int
	PriceYearlyCents     int
	MessageLimit         *int
	SeatLimit            *int
	Features             map[string]any
	StripePriceIDMonthly *string
	StripePriceIDYearly  *string
	IsActive             bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
