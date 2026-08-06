// Package clickapi is the client-side counterpart to Click (click.uz), a
// Uzbek payment provider. link.go builds the redirect URL documented at
// https://docs.click.uz/en/click-button/, given an amount and the org's own
// merchant_id/service_id (entity.ClickIntegration) — no HTTP call, pure URL
// construction. webhook.go is the other half: verifying and responding to
// Click's own Prepare/Complete Shop API callback (the "Merchant API"/"Shop
// API" scheme click-integration-php and similar SDKs implement), used by
// internal/usecase/payment.WebhookUseCase to confirm a payment actually
// happened rather than just that a link was generated. This package still
// does not implement a full merchant API client (no order-creation call,
// no card/invoice flows — see click-integration-php for that scheme) — just
// the redirect-link half and the confirmation-webhook half this codebase
// actually uses.
package clickapi

import (
	"fmt"
	"net/url"
)

// payBaseURL is Click's documented redirect-to-pay endpoint — see
// https://docs.click.uz/en/click-button/ ("Option 1 – redirect by link").
const payBaseURL = "https://my.click.uz/services/pay"

// PaymentLinkInput mirrors the mandatory/optional parameters from Click's
// own table exactly (mandatory: merchant_id, service_id, amount,
// transaction_param; optional: merchant_user_id, return_url, card_type —
// card_type isn't exposed here, nothing in this codebase needs to force a
// card network).
type PaymentLinkInput struct {
	MerchantID     string
	ServiceID      string
	MerchantUserID string // optional — omitted from the link if empty
	// Amount must already be formatted "N.NN" (see FormatAmount) — this
	// package does no currency-unit conversion itself, so a caller can't
	// accidentally pass raw price_cents through unformatted without it
	// being obviously wrong in the resulting URL.
	Amount string
	// TransactionParam is Click's merchant-side order/reference id —
	// mandatory. internal/usecase/ai.buildProductContext passes
	// "{conversationID}-{productID}" (two hyphenated UUID strings, each a
	// fixed 36 characters — see webhook.go's parseTransactionParam for how
	// that's split back apart), deterministic per conversation+product so
	// building the same link twice in one conversation resolves to the same
	// eventual order rather than creating a duplicate.
	TransactionParam string
	ReturnURL        string // optional
}

// BuildPaymentLink returns a ready-to-use Click checkout URL. Pure and
// side-effect-free — no HTTP call, unlike metaapi.Client's methods — so
// internal/usecase/ai can call it directly while building a prompt, no
// Generator/port abstraction needed.
func BuildPaymentLink(in PaymentLinkInput) string {
	q := url.Values{}
	q.Set("merchant_id", in.MerchantID)
	q.Set("service_id", in.ServiceID)
	if in.MerchantUserID != "" {
		q.Set("merchant_user_id", in.MerchantUserID)
	}
	q.Set("amount", in.Amount)
	q.Set("transaction_param", in.TransactionParam)
	if in.ReturnURL != "" {
		q.Set("return_url", in.ReturnURL)
	}
	return payBaseURL + "?" + q.Encode()
}

// FormatAmount converts an integer smallest-currency-unit amount (the
// entity.Product.PriceCents convention — tiyin, for UZS) into Click's
// required "N.NN" decimal string. Integer division/modulo, not a float
// conversion, so this can never introduce floating-point rounding error
// into a payment amount.
func FormatAmount(priceCents int64) string {
	if priceCents < 0 {
		priceCents = 0
	}
	return fmt.Sprintf("%d.%02d", priceCents/100, priceCents%100)
}
