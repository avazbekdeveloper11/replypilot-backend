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

// profileResponse.UserID is the field that matters most here, and it is NOT
// the same ID as the one /oauth/access_token hands back.
//
// Instagram API with Instagram Login has TWO distinct IDs per account:
//
//	app-scoped ID  — what the token exchange returns as "user_id"
//	                 (e.g. 38336100095988690). Fine for Graph calls made
//	                 with that same token, useless for anything else.
//	IG Business ID — what `?fields=user_id` returns (e.g. 17841480194544442).
//	                 This is the ID Meta puts in webhook `entry.id`.
//
// Storing the first one and then looking accounts up by webhook entry.id
// (the second one) never matches — every incoming DM silently falls through
// WebhookUseCase.ingestMessage's not-found branch and is dropped, with the
// webhook still marked `processed`. Nothing errors, nothing logs, the AI
// simply never replies. So FetchProfile resolves both and the caller
// persists the IG Business ID.
//
// The number-vs-string quirk documented on shortLivedTokenResponse shows up
// here too, but INVERTED and inconsistently: /oauth/access_token returns
// user_id as a bare JSON number, while {graphBase}/{id}?fields=user_id
// returns the same logical field as a quoted string. Meta does not document
// this difference and it is not stable enough to rely on either way, hence
// flexibleID below rather than int64 or string.
type profileResponse struct {
	Username string     `json:"username"`
	UserID   flexibleID `json:"user_id"`
}

// flexibleID unmarshals a JSON value that may be either a quoted string or a
// bare number into a string, without caring which it was. Meta returns IDs
// both ways across endpoints (see profileResponse), and a plain int64 field
// fails outright on the string form:
//
//	json: cannot unmarshal string into Go struct field ... of type int64
type flexibleID string

func (f *flexibleID) UnmarshalJSON(b []byte) error {
	*f = flexibleID(strings.Trim(string(b), `"`))
	return nil
}

// FetchProfile: GET {graphBase}/{ig-user-id}?fields=username,user_id
//
// Returns the username and the account's IG Business ID (the webhook-facing
// one — see profileResponse's doc comment). igUserID may be the app-scoped
// ID from the token exchange; `me` would work equally well here since the
// token already scopes the request to one account.
func (c *Client) FetchProfile(ctx context.Context, accessToken, igUserID string) (username, igBusinessID string, err error) {
	u := fmt.Sprintf("%s/%s?fields=username,user_id&access_token=%s", c.graphBase, igUserID, url.QueryEscape(accessToken))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}

	var result profileResponse
	if err := c.doJSON(req, &result); err != nil {
		return "", "", err
	}
	if result.UserID == "" {
		// Defensive: an empty user_id would mean silently reintroducing the
		// exact ID mismatch this method exists to prevent.
		return "", "", fmt.Errorf("instagram profile response missing user_id for %s", igUserID)
	}
	return result.Username, string(result.UserID), nil
}

// FetchCustomerUsername: GET {graphBase}/{igsid}?fields=username
//
// This is a DIFFERENT Graph API call from FetchProfile above, despite
// looking similar, and the two are NOT interchangeable: `user_id` is only
// a valid field when the node being queried is the connecting business's
// own account (FetchProfile's job, during OAuth connect). Querying it for
// an arbitrary message sender's IGSID — a random customer, not a business
// account — gets the whole request rejected outright:
//
//	meta graph api error (http 400, code 100, subcode 0, type
//	IGApiException): Tried accessing nonexisting field (user_id)
//
// discovered live in production: WebhookUseCase.ingestMessage's
// username-resolve reused FetchProfile for this and got that error on
// every single inbound message, silently swallowed (no logging existed
// yet either), so customer usernames never resolved and nothing pointed
// at why. `username` alone is fine on this node — this method exists so
// that call site never asks for a field this endpoint doesn't support.
func (c *Client) FetchCustomerUsername(ctx context.Context, accessToken, igsid string) (string, error) {
	u := fmt.Sprintf("%s/%s?fields=username&access_token=%s", c.graphBase, igsid, url.QueryEscape(accessToken))

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

// maxAttachmentBytes caps DownloadAttachment's response size. Gemini's
// generateContent request body has its own ~20MB overall limit for inline
// (non-Files-API) data, and a base64-encoded image is ~1.33x its raw
// size — 15MB of raw bytes leaves headroom for the rest of the request
// (system prompt, transcript) without needing to account for that
// expansion precisely. A DM photo is normally a few hundred KB to a low
// single-digit MB, so this should never bite in practice; it exists as a
// backstop against an unexpectedly large file rather than a tuned limit.
const maxAttachmentBytes = 15 * 1024 * 1024

// DownloadAttachment fetches the raw bytes of a customer-sent attachment
// (image/video/audio/file) from the CDN URL Meta's webhook delivered in
// message.attachments[].payload.url — see webhookAttachmentPayload's doc
// comment in internal/usecase/instagram on why that URL must be treated as
// short-lived and fetched promptly rather than cached for later. No
// access token is sent with this request: Meta's attachment CDN links are
// themselves pre-signed/scoped, unlike the Graph API endpoints elsewhere
// in this client that require ?access_token=.
//
// Returns the response's declared Content-Type as mimeType, trusted as-is
// (not re-sniffed from the bytes) since Meta's CDN sets it correctly for
// exactly the media types this pipeline cares about.
func (c *Client) DownloadAttachment(ctx context.Context, url string) (data []byte, mimeType string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download attachment: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("download attachment: %w", err)
	}
	if len(body) > maxAttachmentBytes {
		return nil, "", fmt.Errorf("download attachment: exceeds %d byte limit", maxAttachmentBytes)
	}

	return body, resp.Header.Get("Content-Type"), nil
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
