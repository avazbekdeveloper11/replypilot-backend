// Package resendapi is the concrete adapter for Resend's transactional
// email API (resend.com) — plain net/http, no SDK, same reasoning as
// internal/integration/geminiapi and internal/integration/metaapi: this
// codebase has no Go toolchain available in its working environment to
// verify a new dependency resolves and compiles, so every external API
// integration here is a small hand-written REST client instead.
//
// Used by internal/platform/notify.ResendNotifier to deliver registration
// and password-reset verification codes — see
// internal/usecase/auth.UseCase's RequestRegistrationCode and
// ForgotPassword.
//
// IMPORTANT — sending domain: Resend will only deliver to arbitrary
// recipient addresses once a sending domain has been verified in the
// Resend dashboard (SPF/DKIM DNS records). Without that, Resend's shared
// onboarding@resend.dev sender only delivers to the email address on the
// Resend account itself — real signups from other people's inboxes will
// silently fail deliverability (not a client-side error) until a domain
// is verified. See README.md in this directory... there isn't one yet;
// this comment is the record until one exists.
package resendapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const baseURL = "https://api.resend.com"

// Client sends transactional email via Resend. apiKey and from are fixed
// at construction — unlike geminiapi.Client's API key, there's no
// platform-settings-driven hot-swap requirement for this one (email
// delivery isn't rotated from the admin panel the way the Gemini key is),
// so a plain field is enough here.
type Client struct {
	httpClient *http.Client
	apiKey     string
	// from is the verified sender identity, e.g. "ReplyPilot <noreply@yourdomain.com>"
	// — see the package doc comment on domain verification.
	from string
}

func NewClient(apiKey, from string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiKey:     apiKey,
		from:       from,
	}
}

type sendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

type sendResponse struct {
	ID string `json:"id"`
}

// errorResponse mirrors Resend's documented error shape
// (api.resend.com/emails -> 4xx/5xx body: {"statusCode","name","message"}).
// Surfaced in the returned error so a bad `from` (unverified domain) or a
// rejected recipient shows up as an actionable message in worker/handler
// logs instead of a bare "resend api returned 422".
type errorResponse struct {
	StatusCode int    `json:"statusCode"`
	Name       string `json:"name"`
	Message    string `json:"message"`
}

// Send delivers one HTML email via Resend's /emails endpoint. Plain-text
// fallback isn't sent — every caller in this codebase (verification
// codes) sends a short, simple HTML body where that trade-off is fine;
// revisit if a future email genuinely needs a text/plain alternative for
// deliverability reasons.
func (c *Client) Send(ctx context.Context, to, subject, htmlBody string) error {
	reqBody, err := json.Marshal(sendRequest{
		From:    c.from,
		To:      []string{to},
		Subject: subject,
		HTML:    htmlBody,
	})
	if err != nil {
		return fmt.Errorf("marshal resend request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/emails", bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("resend request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read resend response: %w", err)
	}

	if resp.StatusCode >= 300 {
		var apiErr errorResponse
		if jsonErr := json.Unmarshal(body, &apiErr); jsonErr == nil && apiErr.Message != "" {
			return fmt.Errorf("resend api returned %d (%s): %s", resp.StatusCode, apiErr.Name, apiErr.Message)
		}
		return fmt.Errorf("resend api returned %d: %s", resp.StatusCode, string(body))
	}

	var result sendResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("unmarshal resend response: %w", err)
	}
	if result.ID == "" {
		return fmt.Errorf("resend api: response missing email id")
	}
	return nil
}
