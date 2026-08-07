// Package amocrmapi is the concrete adapter for amoCRM's REST API
// (api/v4) and its OAuth 2.0 token endpoint, satisfying the
// amocrm.APIClient port used by internal/usecase/amocrm. It makes real
// HTTP calls to amoCRM's actual endpoints — this is not a mock.
//
// Every call is per-subdomain (https://{subdomain}.amocrm.ru/...) —
// unlike Meta's Graph API, amoCRM has no single global API host; the
// subdomain the org picked when authorizing is part of every URL,
// including the token endpoint itself. See
// https://developers.kommo.com/docs/oauth-20 (Kommo is amoCRM's
// international rebrand — the API is structurally identical; this
// codebase targets amocrm.ru specifically since that's the domain this
// product's market — Uzbekistan/CIS — actually uses).
package amocrmapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	httpClient   *http.Client
	clientID     string
	clientSecret string
	redirectURL  string
}

func NewClient(clientID, clientSecret, redirectURL string) *Client {
	return &Client{
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
	}
}

func baseURL(subdomain string) string {
	return fmt.Sprintf("https://%s.amocrm.ru", subdomain)
}

type tokenResponse struct {
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// ExchangeCode: POST https://{subdomain}.amocrm.ru/oauth2/access_token
// with grant_type=authorization_code. The authorization code is
// single-use and expires 20 minutes after issuance — see
// usecase/amocrm.OAuthUseCase.Complete, which calls this immediately
// after the CSRF state check, no earlier. Returns plain values, not a
// struct — matching internal/integration/metaapi's
// ExchangeCodeForShortLivedToken shape, which the port interface this
// satisfies (amocrm.APIClient) is deliberately modeled on, so the
// usecase layer never imports this package's types.
func (c *Client) ExchangeCode(ctx context.Context, subdomain, code string) (accessToken, refreshToken string, expiresIn time.Duration, err error) {
	return c.exchangeToken(ctx, subdomain, map[string]string{
		"grant_type": "authorization_code",
		"code":       code,
	})
}

// RefreshToken: POST .../oauth2/access_token with grant_type=refresh_token.
// amoCRM issues a NEW refresh token on every successful call — the
// caller (usecase/amocrm) must persist the returned refreshToken, not
// keep reusing the one it started with, or the next refresh will fail
// once amoCRM eventually invalidates the old one.
func (c *Client) RefreshToken(ctx context.Context, subdomain, refreshToken string) (accessToken, newRefreshToken string, expiresIn time.Duration, err error) {
	return c.exchangeToken(ctx, subdomain, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	})
}

func (c *Client) exchangeToken(ctx context.Context, subdomain string, grantFields map[string]string) (accessToken, refreshToken string, expiresIn time.Duration, err error) {
	body := map[string]string{
		"client_id":     c.clientID,
		"client_secret": c.clientSecret,
		"redirect_uri":  c.redirectURL,
	}
	for k, v := range grantFields {
		body[k] = v
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", "", 0, err
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		baseURL(subdomain)+"/oauth2/access_token",
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	var result tokenResponse
	if err := c.doJSON(req, &result); err != nil {
		return "", "", 0, err
	}
	return result.AccessToken, result.RefreshToken, time.Duration(result.ExpiresIn) * time.Second, nil
}

type contactPayload struct {
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name"`
}

type contactsResponse struct {
	Embedded struct {
		Contacts []struct {
			ID int64 `json:"id"`
		} `json:"contacts"`
	} `json:"_embedded"`
}

// CreateContact: POST .../api/v4/contacts. Returns the new contact's
// amoCRM id — the caller (usecase/amocrm.SyncUseCase) persists it in
// amocrm_contact_links so the next sync for the same customer calls
// UpdateContact instead of creating a duplicate.
func (c *Client) CreateContact(ctx context.Context, subdomain, accessToken, name string) (int64, error) {
	payload, err := json.Marshal([]contactPayload{{Name: name}})
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(subdomain)+"/api/v4/contacts", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	c.authorize(req, accessToken)

	var result contactsResponse
	if err := c.doJSON(req, &result); err != nil {
		return 0, err
	}
	if len(result.Embedded.Contacts) == 0 {
		return 0, fmt.Errorf("amocrm: create contact returned no contacts")
	}
	return result.Embedded.Contacts[0].ID, nil
}

// UpdateContact: PATCH .../api/v4/contacts (amoCRM's bulk-update
// endpoint, called here with a single-element array — there is no
// single-contact PATCH-by-path variant in api/v4, unlike leads/notes).
func (c *Client) UpdateContact(ctx context.Context, subdomain, accessToken string, contactID int64, name string) error {
	payload, err := json.Marshal([]contactPayload{{ID: contactID, Name: name}})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, baseURL(subdomain)+"/api/v4/contacts", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.authorize(req, accessToken)

	return c.doJSON(req, nil)
}

type notePayload struct {
	NoteType string `json:"note_type"`
	Params   struct {
		Text string `json:"text"`
	} `json:"params"`
}

// AddNote: POST .../api/v4/contacts/{id}/notes — a "common" (plain
// text) note, used to record a purchase-history summary against the
// contact each time SyncUseCase runs. amoCRM keeps every note ever
// added (there's no "replace the last note" concept), so this
// accumulates a small timeline rather than overwriting — acceptable for
// v1's manual/on-demand sync trigger, not something that would be fine
// on every single order if this were ever wired to fire automatically.
func (c *Client) AddNote(ctx context.Context, subdomain, accessToken string, contactID int64, text string) error {
	note := notePayload{NoteType: "common"}
	note.Params.Text = text

	payload, err := json.Marshal([]notePayload{note})
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v4/contacts/%d/notes", contactID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(subdomain)+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.authorize(req, accessToken)

	return c.doJSON(req, nil)
}

func (c *Client) authorize(req *http.Request, accessToken string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
}

// doJSON sends req, and on a 2xx response decodes the body into out
// (skipped entirely when out is nil — several methods here only care
// whether the call succeeded, not its response body). On a non-2xx
// response it parses amoCRM's application/problem+json error shape
// into AmoCRMAPIError so callers can detect an expired/invalid token
// (401) without string-matching, falling back to a plain error if the
// body doesn't parse as that shape.
func (c *Client) doJSON(req *http.Request, out any) error {
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
		var problem AmoCRMAPIError
		if jsonErr := json.Unmarshal(body, &problem); jsonErr == nil && (problem.Title != "" || problem.Detail != "") {
			problem.HTTPStatus = resp.StatusCode
			return &problem
		}
		return fmt.Errorf("amocrm api returned %d: %s", resp.StatusCode, string(body))
	}

	if out == nil || len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// AmoCRMAPIError is the typed shape of an application/problem+json error
// response — see https://developers.kommo.com. IsAuthError distinguishes
// "the access token is expired/invalid" (401 — usecase/amocrm's sync
// path should refresh and retry once) from every other failure (4xx
// validation errors, 5xx — not retryable by refreshing).
type AmoCRMAPIError struct {
	HTTPStatus int    `json:"-"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Status     int    `json:"status"`
	Detail     string `json:"detail"`
}

func (e *AmoCRMAPIError) Error() string {
	return fmt.Sprintf("amocrm api error (http %d): %s — %s", e.HTTPStatus, e.Title, e.Detail)
}

func (e *AmoCRMAPIError) IsAuthError() bool {
	return e.HTTPStatus == http.StatusUnauthorized
}
