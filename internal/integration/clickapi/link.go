// Package clickapi builds payment links for Click (click.uz), a Uzbek
// payment provider. This is deliberately NOT a full merchant API client —
// no order-creation call, no webhook-signature verification, no
// PREPARE/COMPLETE handshake (the "Shop API" scheme click-integration-php
// and similar SDKs implement). It's exactly one thing: build the redirect
// URL documented at https://docs.click.uz/en/click-button/, given an
// amount and the org's own merchant_id/service_id
// (entity.ClickIntegration). Confirming a payment actually happened (via
// Click's PREPARE/COMPLETE webhook) is real, separate follow-up work, not
// implemented here — see internal/usecase/ai's product-context doc comment
// for the current scope.
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
	// mandatory, but this codebase has no order/invoice entity yet, so
	// callers pass a freshly generated identifier (e.g. a UUID). It is
	// never looked up again — there is no PREPARE/COMPLETE webhook handler
	// here to match it against (see package doc comment).
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
