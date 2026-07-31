// Package stripeapi is the concrete adapter for Stripe's REST API —
// Checkout Sessions (self-serve subscribe) and Billing Portal Sessions
// (self-serve manage/cancel/update payment method), plus webhook signature
// verification. Plain net/http, no stripe-go SDK dependency — same
// reasoning as internal/integration/metaapi and geminiapi: no Go toolchain
// available anywhere in this codebase's development environment to verify
// a new dependency resolves and compiles.
//
// Stripe's REST API is form-encoded (application/x-www-form-urlencoded),
// NOT JSON, for requests — responses are JSON. This client reflects that;
// don't "fix" the request encoding to JSON, it would not work against
// Stripe's actual API.
package stripeapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://api.stripe.com/v1"

type Client struct {
	httpClient *http.Client
	secretKey  string
}

func NewClient(secretKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		secretKey:  secretKey,
	}
}

type CheckoutSessionParams struct {
	PriceID        string
	CustomerEmail  string
	SuccessURL     string
	CancelURL      string
	OrganizationID string // written into the session's metadata, read back on checkout.session.completed
}

type checkoutSessionResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreateCheckoutSession: POST /v1/checkout/sessions, mode=subscription,
// one line item. Stripe hosts the entire payment form — this codebase
// never touches a card number, deliberately (PCI scope).
func (c *Client) CreateCheckoutSession(ctx context.Context, p CheckoutSessionParams) (string, error) {
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("success_url", p.SuccessURL)
	form.Set("cancel_url", p.CancelURL)
	form.Set("customer_email", p.CustomerEmail)
	form.Set("line_items[0][price]", p.PriceID)
	form.Set("line_items[0][quantity]", "1")
	form.Set("metadata[organization_id]", p.OrganizationID)
	// Also stamped onto the subscription itself (not just the session) so
	// customer.subscription.* events — which don't carry the Checkout
	// Session's own metadata — can still be traced back to an org if ever
	// needed for support/debugging. The actual webhook flow doesn't need
	// this for updated/deleted events (it looks up by Stripe subscription
	// id instead, see SubscriptionRepository.FindByStripeSubscriptionID),
	// but it costs nothing to carry and helps when reading raw Stripe
	// dashboard data.
	form.Set("subscription_data[metadata][organization_id]", p.OrganizationID)

	var result checkoutSessionResponse
	if err := c.doForm(ctx, http.MethodPost, "/checkout/sessions", form, &result); err != nil {
		return "", fmt.Errorf("create checkout session: %w", err)
	}
	return result.URL, nil
}

type portalSessionResponse struct {
	URL string `json:"url"`
}

// CreatePortalSession: POST /v1/billing_portal/sessions — the Stripe-hosted
// page a customer uses to update their payment method, view invoice
// history, or cancel. Building an equivalent in-app UI for all of that
// would duplicate what Stripe already provides and is out of scope here;
// see backend/README.md's known-gaps list.
func (c *Client) CreatePortalSession(ctx context.Context, stripeCustomerID, returnURL string) (string, error) {
	form := url.Values{}
	form.Set("customer", stripeCustomerID)
	form.Set("return_url", returnURL)

	var result portalSessionResponse
	if err := c.doForm(ctx, http.MethodPost, "/billing_portal/sessions", form, &result); err != nil {
		return "", fmt.Errorf("create billing portal session: %w", err)
	}
	return result.URL, nil
}

func (c *Client) doForm(ctx context.Context, method, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Stripe auth: HTTP Basic with the secret key as username, empty
	// password — not a bearer token.
	req.SetBasicAuth(c.secretKey, "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("stripe api returned %d: %s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}

// webhookTolerance rejects a webhook whose timestamp is older than this —
// the standard defense Stripe's own SDKs apply against a replayed request
// (an old, previously-valid signed payload replayed by an attacker who
// captured it in transit before TLS, or a compromised intermediary).
const webhookTolerance = 5 * time.Minute

// VerifyWebhookSignature implements Stripe's documented signature scheme
// by hand (https://stripe.com/docs/webhooks#verify-manually) since this
// codebase has no stripe-go dependency to call webhook.ConstructEvent with.
// The Stripe-Signature header looks like "t=<unix-ts>,v1=<hex-hmac>[,v0=...]".
// The signed payload is "<ts>.<raw body>", HMAC-SHA256'd with the webhook
// signing secret (whsec_...). Constant-time compare against every v1
// value present (Stripe can send multiple during secret rotation).
func VerifyWebhookSignature(payload []byte, sigHeader, webhookSecret string) error {
	var timestamp string
	var signatures []string

	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			signatures = append(signatures, kv[1])
		}
	}
	if timestamp == "" || len(signatures) == 0 {
		return fmt.Errorf("stripe webhook: malformed Stripe-Signature header")
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("stripe webhook: invalid timestamp: %w", err)
	}
	if time.Since(time.Unix(ts, 0)).Abs() > webhookTolerance {
		return fmt.Errorf("stripe webhook: timestamp outside tolerance window")
	}

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(timestamp + "." + string(payload)))
	expected := hex.EncodeToString(mac.Sum(nil))

	for _, sig := range signatures {
		if hmac.Equal([]byte(sig), []byte(expected)) {
			return nil
		}
	}
	return fmt.Errorf("stripe webhook: signature mismatch")
}
