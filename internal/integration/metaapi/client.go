// Package metaapi is the concrete adapter for Meta's Instagram Graph API,
// satisfying the instagram.GraphAPIClient port used by
// internal/usecase/instagram. It makes real HTTP calls to Meta's actual
// endpoints — this is not a mock.
package metaapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	httpClient  *http.Client
	appID       string
	appSecret   string
	redirectURL string
	graphBase   string // e.g. https://graph.instagram.com
}

func NewClient(appID, appSecret, redirectURL, graphBase string) *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		appID:       appID,
		appSecret:   appSecret,
		redirectURL: redirectURL,
		graphBase:   graphBase,
	}
}

// UserID comes back as a JSON *number* from this endpoint (e.g.
// "user_id": 17841400008460056), not a string — discovered the hard way
// (a live "cannot unmarshal number into Go struct field ... of type
// string" error) despite every other Meta/Instagram ID in this codebase
// being string-typed. int64 holds it exactly (Instagram-scoped IDs are
// well under int64's range); ExchangeCodeForShortLivedToken still
// returns a string, matching entity.InstagramAccount.IGUserID and every
// other GraphAPIClient method's igUserID parameter — the number-vs-string
// mismatch is contained entirely inside this one response struct.
type shortLivedTokenResponse struct {
	AccessToken string `json:"access_token"`
	UserID      int64  `json:"user_id"`
}

// ExchangeCodeForShortLivedToken: POST https://api.instagram.com/oauth/access_token
func (c *Client) ExchangeCodeForShortLivedToken(ctx context.Context, code string) (string, string, error) {
	form := url.Values{}
	form.Set("client_id", c.appID)
	form.Set("client_secret", c.appSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", c.redirectURL)
	form.Set("code", code)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		"https://api.instagram.com/oauth/access_token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var result shortLivedTokenResponse
	if err := c.doJSON(req, &result); err != nil {
		return "", "", err
	}
	return result.AccessToken, strconv.FormatInt(result.UserID, 10), nil
}

type longLivedTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// ExchangeForLongLivedToken: GET {graphBase}/access_token?grant_type=ig_exchange_token
// Long-lived tokens are valid ~60 days from issuance and must be refreshed
// (not re-exchanged) before they expire — see RefreshLongLivedToken.
func (c *Client) ExchangeForLongLivedToken(ctx context.Context, shortLivedToken string) (string, time.Duration, error) {
	u := fmt.Sprintf(
		"%s/access_token?grant_type=ig_exchange_token&client_secret=%s&access_token=%s",
		c.graphBase, url.QueryEscape(c.appSecret), url.QueryEscape(shortLivedToken),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, err
	}

	var result longLivedTokenResponse
	if err := c.doJSON(req, &result); err != nil {
		return "", 0, err
	}
	return result.AccessToken, time.Duration(result.ExpiresIn) * time.Second, nil
}

// RefreshLongLivedToken: GET {graphBase}/refresh_access_token?grant_type=ig_refresh_token
// Called by a scheduled job (not part of this API service) against every
// InstagramAccount whose TokenExpiresAt is within the next ~10 days — see
// docs/ARCHITECTURE.md §7 and the idx_instagram_accounts_token_expiry index
// in database/schema.sql, which exists specifically to make that job's
// query cheap.
func (c *Client) RefreshLongLivedToken(ctx context.Context, currentToken string) (string, time.Duration, error) {
	u := fmt.Sprintf(
		"%s/refresh_access_token?grant_type=ig_refresh_token&access_token=%s",
		c.graphBase, url.QueryEscape(currentToken),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", 0, err
	}

	var result longLivedTokenResponse
	if err := c.doJSON(req, &result); err != nil {
		return "", 0, err
	}
	return result.AccessToken, time.Duration(result.ExpiresIn) * time.Second, nil
}

type profileResponse struct {
	Username string `json:"username"`
}

// FetchProfile: GET {graphBase}/{ig-user-id}?fields=username
func (c *Client) FetchProfile(ctx context.Context, accessToken, igUserID string) (string, error) {
	u := fmt.Sprintf("%s/%s?fields=username&access_token=%s", c.graphBase, igUserID, url.QueryEscape(accessToken))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}

	var result profileResponse
	if err := c.doJSON(req, &result); err != nil {
		return "", err
	}
	return result.Username, nil
}

type subscribedAppsResponse struct {
	Success bool `json:"success"`
}

// SubscribeApp subscribes the connected Instagram account to the given
// webhook fields via POST {graphBase}/{ig-user-id}/subscribed_apps.
//
// This is the step that's easy to forget and silently breaks everything:
// completing OAuth does NOT make an account send webhooks. Without this
// call, the account is connected and the token works, but no `messages`
// event ever reaches the webhook receiver — the whole DM pipeline is dead
// for that account with no error to point at. See
// docs/META_GRAPH_API_REFERENCE.md §3.
//
// fields is the comma-separated field list, e.g. "messages,messaging_seen".
func (c *Client) SubscribeApp(ctx context.Context, accessToken, igUserID, fields string) error {
	u := fmt.Sprintf(
		"%s/%s/subscribed_apps?subscribed_fields=%s&access_token=%s",
		c.graphBase, igUserID, url.QueryEscape(fields), url.QueryEscape(accessToken),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}

	var result subscribedAppsResponse
	if err := c.doJSON(req, &result); err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("meta subscribed_apps returned success=false for ig user %s", igUserID)
	}
	return nil
}

type sendMessageRequest struct {
	Recipient sendMessageRecipient `json:"recipient"`
	Message   sendMessageBody      `json:"message"`
}

type sendMessageRecipient struct {
	ID string `json:"id"`
}

type sendMessageBody struct {
	Text string `json:"text"`
}

type sendMessageResponse struct {
	RecipientID string `json:"recipient_id"`
	MessageID   string `json:"message_id"`
}

// SendMessage sends a text DM to a customer via POST {graphBase}/me/messages
// — Instagram's Send API (same shape as the Messenger Platform's). Used by
// internal/usecase/ai to deliver the AI-generated reply. accessToken is the
// SENDING account's own long-lived token (the InstagramAccount that owns
// the conversation), decrypted by the caller — this client never touches
// pkg/crypto, same separation of concerns as everywhere else it's used.
func (c *Client) SendMessage(ctx context.Context, accessToken, recipientIGID, text string) error {
	reqBody, err := json.Marshal(sendMessageRequest{
		Recipient: sendMessageRecipient{ID: recipientIGID},
		Message:   sendMessageBody{Text: text},
	})
	if err != nil {
		return err
	}

	u := fmt.Sprintf("%s/me/messages?access_token=%s", c.graphBase, url.QueryEscape(accessToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	var result sendMessageResponse
	if err := c.doJSON(req, &result); err != nil {
		return err
	}
	if result.MessageID == "" {
		return fmt.Errorf("meta send message: no message_id in response")
	}
	return nil
}

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
		// Meta's error responses are consistently shaped as
		// {"error": {"message","type","code","error_subcode",...}} — parse
		// into GraphAPIError so callers (e.g. internal/usecase/ai) can act on
		// the error code instead of string-matching. If the body doesn't
		// parse as that shape (network edge cases, a non-Graph endpoint),
		// fall back to the old plain error.
		var envelope graphErrorEnvelope
		if jsonErr := json.Unmarshal(body, &envelope); jsonErr == nil && envelope.Error != nil {
			envelope.Error.HTTPStatus = resp.StatusCode
			return envelope.Error
		}
		return fmt.Errorf("meta graph api returned %d: %s", resp.StatusCode, string(body))
	}

	return json.Unmarshal(body, out)
}

// GraphAPIError is the typed shape of an error response from Meta's Graph
// API. Returned by doJSON instead of a bare fmt.Errorf whenever the
// response body parses into this shape, specifically so callers can detect
// an invalid/expired/revoked access token (code 190 — see
// https://developers.facebook.com/docs/graph-api/guides/error-handling)
// without string-matching an error message. Used by
// internal/usecase/ai.HandleInboundMessage to flip InstagramAccount.Status
// when a send fails because the token stopped working.
type GraphAPIError struct {
	HTTPStatus int    `json:"-"`
	Message    string `json:"message"`
	Type       string `json:"type"`
	Code       int    `json:"code"`
	Subcode    int    `json:"error_subcode"`
	FBTraceID  string `json:"fbtrace_id"`
}

func (e *GraphAPIError) Error() string {
	return fmt.Sprintf("meta graph api error (http %d, code %d, subcode %d, type %s): %s",
		e.HTTPStatus, e.Code, e.Subcode, e.Type, e.Message)
}

// IsAuthError reports whether this is Meta's standard "the access token is
// invalid, expired, or was revoked" error — code 190 covers all three; the
// subcode (see IsExpired) is what distinguishes them.
func (e *GraphAPIError) IsAuthError() bool {
	return e.Code == 190
}

// IsExpired reports whether subcode 463 was given — Meta's specific signal
// that the token simply ran past its ~60-day lifetime, as opposed to any
// other 190 subcode (app deauthorized, password changed, malformed/invalid
// token). Only meaningful when IsAuthError() is true. The distinction
// matters to the caller: an expired token is what cmd/token-refresh exists
// to prevent, while every other 190 means the user must reconnect from
// scratch via OAuth.
func (e *GraphAPIError) IsExpired() bool {
	return e.Subcode == 463
}

type graphErrorEnvelope struct {
	Error *GraphAPIError `json:"error"`
}
