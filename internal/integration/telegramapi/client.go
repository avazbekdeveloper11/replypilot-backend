// Package telegramapi is the concrete adapter for Telegram's Bot API,
// specifically the Business Bot surface (sendMessage/updates carrying a
// business_connection_id) that lets a bot reply on behalf of a real
// person's Telegram account — see migration 000014's header comment for
// how that feature works end to end. Makes real HTTP calls to
// api.telegram.org; this is not a mock.
package telegramapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type Client struct {
	httpClient *http.Client
	apiBase    string // e.g. https://api.telegram.org — overridable for tests
}

func NewClient(apiBase string) *Client {
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiBase:    apiBase,
	}
}

// apiResponse is the envelope every Bot API method responds with —
// {"ok": true, "result": ...} on success, {"ok": false, "description": ...}
// on failure. Unlike Meta's Graph API, Telegram always returns HTTP 200 for
// well-formed-but-rejected requests (bad token, chat not found, etc.) and
// signals failure via "ok" instead — doJSON below checks ok, not status.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// APIError is returned by doJSON when Telegram answers ok=false. ErrorCode
// mirrors Telegram's error_code (401 = bad/revoked bot token, 403 = bot
// blocked/kicked, 400 covers most "bad request" shapes including "chat not
// found").
type APIError struct {
	ErrorCode   int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram bot api error (code %d): %s", e.ErrorCode, e.Description)
}

// IsAuthError reports whether the bot token itself is invalid or was
// revoked (BotFather's /revoke, or the bot being deleted) — Telegram's
// signal for this is error_code 401, the direct counterpart to Meta's
// GraphAPIError.IsAuthError (code 190). Used by callers to flip
// TelegramAccount.Status the same way internal/usecase/ai already does for
// Instagram; see that package's authError interface.
func (e *APIError) IsAuthError() bool {
	return e.ErrorCode == 401
}

type meResult struct {
	Username string `json:"username"`
}

// GetMe: GET {apiBase}/bot{token}/getMe — validates a bot token is real and
// returns the bot's own @username, called once at connect time
// (telegram.ConnectUseCase.Connect) so a pasted garbage string fails
// immediately with a clear error instead of silently saving a dead token.
func (c *Client) GetMe(ctx context.Context, botToken string) (username string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.methodURL(botToken, "getMe"), nil)
	if err != nil {
		return "", err
	}

	var result meResult
	if err := c.doJSON(req, &result); err != nil {
		return "", err
	}
	return result.Username, nil
}

type setWebhookRequest struct {
	URL         string `json:"url"`
	SecretToken string `json:"secret_token,omitempty"`
	// AllowedUpdates restricts delivery to exactly what this codebase
	// handles (telegram.WebhookUseCase) — business_connection to learn the
	// pairing id, business_message for inbound DMs, and (added for admin
	// notifications — see WebhookUseCase.handlePlainMessage) message for the
	// plain, non-business chats an admin uses to send the bot their
	// verification code directly. Without this Telegram defaults to a
	// broader set that would just be ignored on receipt, but pinning it
	// explicitly documents the actual dependency and avoids paying for
	// deliveries nothing reads.
	AllowedUpdates []string `json:"allowed_updates"`
}

// SetWebhook: POST {apiBase}/bot{token}/setWebhook — registers webhookURL
// (expected shape: {WEB_BASE}/webhooks/telegram/{telegram_account_id}, see
// telegram_handler.go) as this bot's delivery endpoint. secretToken, when
// non-empty, is echoed back by Telegram on every delivery as the
// X-Telegram-Bot-Api-Secret-Token header — see config.TelegramConfig's doc
// comment on why this codebase uses one shared secret for all orgs' bots
// rather than a per-bot one.
func (c *Client) SetWebhook(ctx context.Context, botToken, webhookURL, secretToken string) error {
	body, err := json.Marshal(setWebhookRequest{
		URL:            webhookURL,
		SecretToken:    secretToken,
		AllowedUpdates: []string{"business_connection", "business_message", "message"},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(botToken, "setWebhook"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	return c.doJSON(req, nil)
}

type sendMessageRequest struct {
	ChatID               string `json:"chat_id"`
	Text                 string `json:"text"`
	BusinessConnectionID string `json:"business_connection_id"`
}

// SendMessage: POST {apiBase}/bot{token}/sendMessage, with
// business_connection_id set — this is what makes the message appear as
// sent from the connected person's own Telegram account rather than from
// the bot itself. chatID is the customer's chat id (see
// entity.Conversation.CustomerIGID's doc comment on why it's a string here
// despite Telegram chat ids being numeric — sendMessage's chat_id field
// accepts either JSON type, so no parse/convert step is needed).
func (c *Client) SendMessage(ctx context.Context, botToken, businessConnectionID, chatID, text string) error {
	body, err := json.Marshal(sendMessageRequest{
		ChatID:               chatID,
		Text:                 text,
		BusinessConnectionID: businessConnectionID,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(botToken, "sendMessage"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	return c.doJSON(req, nil)
}

type sendPlainMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// SendPlainMessage: POST {apiBase}/bot{token}/sendMessage, with no
// business_connection_id — used only for admin-facing notifications sent
// by the bot itself (see telegram.NotifyUseCase), never for customer-facing
// replies (those go through SendMessage). Kept as a separate method with
// its own request struct rather than calling SendMessage with an empty
// businessConnectionID: sendMessageRequest.BusinessConnectionID has no
// `omitempty`, so reusing it here would send business_connection_id: "" to
// Telegram, which is untested/undocumented behavior — a plain sendMessage
// call simply omits the field entirely, which is Telegram's documented,
// unambiguous way to send as the bot itself.
func (c *Client) SendPlainMessage(ctx context.Context, botToken string, chatID int64, text string) error {
	body, err := json.Marshal(sendPlainMessageRequest{
		ChatID: strconv.FormatInt(chatID, 10),
		Text:   text,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(botToken, "sendMessage"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	return c.doJSON(req, nil)
}

type getFileResult struct {
	FilePath string `json:"file_path"`
}

// ResolveFileURL: GET {apiBase}/bot{token}/getFile?file_id={fileID}, then
// builds the actual download link {apiBase}/file/bot{token}/{file_path}.
// Deliberately returns the URL rather than downloading the bytes itself —
// that URL is a plain, unauthenticated-but-token-scoped GET (the token is
// embedded in the path, not passed separately), so it's fetchable by the
// exact same internal/usecase/ai.MediaFetcher.DownloadAttachment this
// codebase already has wired for Instagram attachments (see that
// interface's doc comment on why one client instance now serves both
// channels). Keeping the token out of a second, separately-typed
// DownloadAttachment method here avoids that duplication entirely.
//
// Per Telegram's docs the returned link is guaranteed valid for at least 1
// hour — called from telegram.WebhookUseCase at ingestion time, same
// "fetch/construct promptly" posture as Meta's CDN attachment URLs (see
// instagram.WebhookUseCase's webhookAttachmentPayload doc comment), so that
// window comfortably covers the time until cmd/worker-ai picks the event
// up.
func (c *Client) ResolveFileURL(ctx context.Context, botToken, fileID string) (string, error) {
	u := fmt.Sprintf("%s/bot%s/getFile?file_id=%s", c.apiBase, botToken, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}

	var result getFileResult
	if err := c.doJSON(req, &result); err != nil {
		return "", err
	}
	if result.FilePath == "" {
		return "", fmt.Errorf("telegram getFile: empty file_path for file_id %s", fileID)
	}
	return fmt.Sprintf("%s/file/bot%s/%s", c.apiBase, botToken, result.FilePath), nil
}

func (c *Client) methodURL(botToken, method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.apiBase, botToken, method)
}

// doJSON sends req and, on ok=true, unmarshals the "result" field into out
// (skipped entirely when out is nil — SetWebhook/SendMessage only care
// whether the call succeeded). On ok=false, returns *APIError so callers
// can inspect ErrorCode (see APIError.IsAuthError).
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

	var envelope apiResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("telegram bot api: unparseable response (http %d): %s", resp.StatusCode, string(body))
	}
	if !envelope.OK {
		return &APIError{ErrorCode: envelope.ErrorCode, Description: envelope.Description}
	}
	if out == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}
