package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// EventPublisher mirrors instagram.EventPublisher — see that type's doc
// comment on why this package declares its own copy rather than importing
// instagram's (usecases don't depend on each other).
type EventPublisher interface {
	Publish(ctx context.Context, routingKey string, payload any) error
}

// FileResolver is the narrow port onto turning a Telegram file_id into a
// downloadable URL — satisfied by internal/integration/telegramapi.Client.ResolveFileURL.
type FileResolver interface {
	ResolveFileURL(ctx context.Context, botToken, fileID string) (string, error)
}

// RoutingKeyDMReceived matches instagram.RoutingKeyDMReceived's value
// exactly — both channels' webhook receivers feed the same worker
// (cmd/worker-ai), which only cares about the routing key string and the
// wire shape (see dmReceivedEvent below), not which package published it.
const RoutingKeyDMReceived = "dm.received"

// dmReceivedEvent mirrors instagram.DMReceivedEvent's JSON shape exactly.
// InstagramAccountID is always the nil UUID here — a Telegram-channel event
// has no Instagram account, but cmd/worker-ai's parseEvent requires every
// field to parse as a UUID, so this is set explicitly to uuid.Nil rather
// than omitted. ai.UseCase.HandleInboundMessage never reads it for a
// Telegram-channel conversation — it resolves the sending account from
// conv.TelegramAccountID instead. See that method's channel switch.
type dmReceivedEvent struct {
	OrganizationID     string `json:"organization_id"`
	ConversationID     string `json:"conversation_id"`
	MessageID          string `json:"message_id"`
	InstagramAccountID string `json:"instagram_account_id"`
}

type WebhookUseCase struct {
	logRepo     repository.WebhookLogRepository
	accountRepo repository.TelegramAccountRepository
	convRepo    repository.ConversationRepository
	messageRepo repository.MessageRepository
	publisher   EventPublisher
	files       FileResolver
	encryptor   *crypto.AESGCMEncryptor
	// webhookSecret, when non-empty, must match every delivery's
	// X-Telegram-Bot-Api-Secret-Token header exactly (see
	// config.TelegramConfig's doc comment). When empty, Process rejects
	// every delivery — fail closed, not fail open, on a misconfigured
	// deployment. Compared with crypto/subtle.ConstantTimeCompare, same
	// posture as pkg/signature's HMAC check for Meta, even though a bare
	// shared-secret compare isn't itself a signature.
	webhookSecret string
	logger        *zap.Logger
}

func NewWebhookUseCase(
	logRepo repository.WebhookLogRepository,
	accountRepo repository.TelegramAccountRepository,
	convRepo repository.ConversationRepository,
	messageRepo repository.MessageRepository,
	publisher EventPublisher,
	files FileResolver,
	encryptor *crypto.AESGCMEncryptor,
	webhookSecret string,
	logger *zap.Logger,
) *WebhookUseCase {
	return &WebhookUseCase{
		logRepo:       logRepo,
		accountRepo:   accountRepo,
		convRepo:      convRepo,
		messageRepo:   messageRepo,
		publisher:     publisher,
		files:         files,
		encryptor:     encryptor,
		webhookSecret: webhookSecret,
		logger:        logger,
	}
}

// --- payload shapes matching Telegram Bot API's Update object ---
// https://core.telegram.org/bots/api#update
// https://core.telegram.org/bots/api#business-features

type telegramUpdate struct {
	UpdateID           int64                `json:"update_id"`
	BusinessConnection *businessConnection  `json:"business_connection,omitempty"`
	BusinessMessage    *businessMessage     `json:"business_message,omitempty"`
}

// businessConnection is delivered once when an org finishes pairing this
// bot in their own Telegram app — see WebhookUseCase.handleBusinessConnection.
type businessConnection struct {
	ID         string `json:"id"`
	User       tgUser `json:"user"`
	UserChatID int64  `json:"user_chat_id"`
	IsEnabled  bool   `json:"is_enabled"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type tgChat struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

// tgPhotoSize/tgFile mirror Telegram's PhotoSize/Voice/Video objects,
// trimmed to the fields ingestMessage actually needs.
type tgPhotoSize struct {
	FileID string `json:"file_id"`
	Width  int    `json:"width"`
}

type tgFile struct {
	FileID string `json:"file_id"`
}

// businessMessage is a customer's inbound DM to a connected business
// account — the Telegram-channel counterpart to Meta's webhookMessaging.
type businessMessage struct {
	MessageID            int64         `json:"message_id"`
	BusinessConnectionID string        `json:"business_connection_id"`
	Chat                  tgChat       `json:"chat"`
	Date                  int64        `json:"date"`
	Text                  string       `json:"text"`
	Photo                 []tgPhotoSize `json:"photo,omitempty"`
	Voice                 *tgFile      `json:"voice,omitempty"`
	Video                 *tgFile      `json:"video,omitempty"`
}

// VerifySecret reports whether headerVal matches the configured webhook
// secret. Fails closed: an empty configured secret (feature never
// configured) never matches anything, same "boots without it, feature
// errors until configured" posture as GeminiConfig/StripeConfig elsewhere
// in this codebase, except here the safe direction is reject, not skip.
func (uc *WebhookUseCase) VerifySecret(headerVal string) bool {
	if uc.webhookSecret == "" || headerVal == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(headerVal), []byte(uc.webhookSecret)) == 1
}

// Process records the delivery in webhook_logs regardless of outcome (same
// audit-trail reasoning as instagram.WebhookUseCase.Process — see that
// method's doc comment) and, for a business_connection or business_message
// update, handles it. accountID comes from the webhook URL path
// (/webhooks/telegram/:id — see telegram_webhook_handler.go), not from
// anything inside the payload: unlike Meta, whose entry.id names the
// account, Telegram's update body doesn't carry any id this codebase
// assigned, so the URL itself is what tells us which org's bot this is.
func (uc *WebhookUseCase) Process(ctx context.Context, accountID uuid.UUID, rawBody []byte) error {
	log := &entity.WebhookLog{
		Source:         entity.WebhookSourceTelegram,
		Payload:        rawBody,
		SignatureValid: true, // secret header already checked by the handler before Process is called
		Status:         entity.WebhookStatusReceived,
	}
	if err := uc.logRepo.Create(ctx, log); err != nil {
		return err
	}

	var update telegramUpdate
	if err := json.Unmarshal(rawBody, &update); err != nil {
		errMsg := err.Error()
		_ = uc.logRepo.UpdateStatus(ctx, log.ID, entity.WebhookStatusFailed, &errMsg)
		return apperror.InvalidInput("malformed telegram webhook payload", err)
	}

	var processErr error
	if update.BusinessConnection != nil {
		processErr = uc.handleBusinessConnection(ctx, accountID, *update.BusinessConnection)
	}
	if update.BusinessMessage != nil {
		if err := uc.ingestMessage(ctx, accountID, *update.BusinessMessage); err != nil {
			processErr = err
		}
	}

	if processErr != nil {
		errMsg := processErr.Error()
		_ = uc.logRepo.UpdateStatus(ctx, log.ID, entity.WebhookStatusFailed, &errMsg)
		return processErr
	}
	return uc.logRepo.UpdateStatus(ctx, log.ID, entity.WebhookStatusProcessed, nil)
}

// handleBusinessConnection records the pairing id the moment the org
// finishes connecting this bot inside their own Telegram app — see
// entity.TelegramAccount.BusinessConnectionID's doc comment. Not found /
// disabled connections are logged and swallowed, not errored: same
// "unrecognized delivery, not our problem" posture as
// instagram.WebhookUseCase.ingestMessage's not-found branch.
func (uc *WebhookUseCase) handleBusinessConnection(ctx context.Context, accountID uuid.UUID, bc businessConnection) error {
	account, err := uc.accountRepo.FindByIDForWebhook(ctx, accountID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil
		}
		return err
	}
	if !bc.IsEnabled {
		// Org disabled/removed the bot from their Chat Automation settings.
		// Left connected here deliberately (not flipped to an error status)
		// — nothing is actually broken, sending will just start failing
		// with Telegram's own "connection disabled" error if it's ever
		// attempted, which handleSendFailure-equivalent logic can react to
		// later; not needed for this MVP.
		return nil
	}

	connID := bc.ID
	account.BusinessConnectionID = &connID
	return uc.accountRepo.Update(ctx, account)
}

func (uc *WebhookUseCase) ingestMessage(ctx context.Context, accountID uuid.UUID, m businessMessage) error {
	account, err := uc.accountRepo.FindByIDForWebhook(ctx, accountID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil
		}
		return err
	}

	chatIDStr := strconv.FormatInt(m.Chat.ID, 10)

	conv, err := uc.convRepo.FindByTelegramAccountAndCustomer(ctx, account.OrganizationID, account.ID, chatIDStr)
	isNewConversation := false
	if err != nil {
		if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
			return err
		}
		conv = &entity.Conversation{
			OrganizationID:    account.OrganizationID,
			Channel:           entity.ConversationChannelTelegram,
			TelegramAccountID: &account.ID,
			CustomerIGID:      chatIDStr,
			Status:            entity.ConversationStatusAIActive,
		}
		if err := uc.convRepo.Create(ctx, conv); err != nil {
			return err
		}
		isNewConversation = true
	}

	// Unlike Instagram (whose webhook payload carries no username at all —
	// see instagram.ProfileFetcher's doc comment), Telegram's Chat object
	// already includes the customer's @username directly when they have a
	// public one set, so there's no separate API call needed to resolve it.
	if (isNewConversation || conv.CustomerUsername == nil) && m.Chat.Username != "" {
		username := m.Chat.Username
		conv.CustomerUsername = &username
	}

	// Prefixed (not a bare decimal) — see this field's doc comment on
	// entity.Message.IGMessageID: it's a single global unique index shared
	// with Instagram's mid values, and Telegram message ids are small
	// integers that could otherwise collide with a numeric-looking mid.
	externalMessageID := fmt.Sprintf("tg:%d", m.MessageID)
	if existing, err := uc.messageRepo.FindByIGMessageID(ctx, account.OrganizationID, externalMessageID); err == nil && existing != nil {
		return nil // already ingested — Telegram redelivered; idempotent no-op
	}

	msgType, attachmentURL := uc.classifyAttachment(ctx, account, m)
	createdAt := time.Unix(m.Date, 0)
	msg := &entity.Message{
		OrganizationID: account.OrganizationID,
		ConversationID: conv.ID,
		Direction:      entity.MessageDirectionInbound,
		SenderType:     entity.MessageSenderCustomer,
		MessageType:    msgType,
		AttachmentURL:  attachmentURL,
		IGMessageID:    &externalMessageID,
		CreatedAt:      createdAt,
	}
	if m.Text != "" {
		text := m.Text
		msg.Content = &text
	}
	if err := uc.messageRepo.Create(ctx, msg); err != nil {
		return err
	}

	preview := previewFor(msg)
	conv.LastMessageAt = &createdAt
	conv.LastMessagePreview = &preview
	conv.UnreadCount++
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return err
	}

	return uc.publisher.Publish(ctx, RoutingKeyDMReceived, dmReceivedEvent{
		OrganizationID:      account.OrganizationID.String(),
		ConversationID:      conv.ID.String(),
		MessageID:           msg.ID.String(),
		InstagramAccountID:  uuid.Nil.String(),
	})
}

// classifyAttachment mirrors instagram.classifyAttachment's role: pick the
// one attachment this message carries (Telegram sends photo as an array of
// progressively larger resolutions — the last entry is the largest, matched
// against how a customer would expect their photo to be seen) and resolve
// it to a fetchable URL via the bot's own token. A resolve failure (bad
// token, Telegram outage) degrades to storing the message as text-only
// rather than failing ingestion outright — same fail-open posture as
// everywhere else in this pipeline that treats "couldn't fetch a
// nice-to-have" as non-fatal.
func (uc *WebhookUseCase) classifyAttachment(ctx context.Context, account *entity.TelegramAccount, m businessMessage) (entity.MessageType, *string) {
	var fileID string
	var msgType entity.MessageType
	switch {
	case len(m.Photo) > 0:
		fileID = m.Photo[len(m.Photo)-1].FileID
		msgType = entity.MessageTypeImage
	case m.Voice != nil:
		fileID = m.Voice.FileID
		msgType = entity.MessageTypeAudio
	case m.Video != nil:
		fileID = m.Video.FileID
		msgType = entity.MessageTypeVideo
	default:
		return entity.MessageTypeText, nil
	}

	botToken, err := uc.encryptor.Decrypt(account.BotTokenEncrypted)
	if err != nil {
		uc.logger.Warn("resolve telegram attachment: decrypt bot token failed",
			zap.String("telegram_account_id", account.ID.String()), zap.Error(err))
		return entity.MessageTypeText, nil
	}
	url, err := uc.files.ResolveFileURL(ctx, botToken, fileID)
	if err != nil {
		uc.logger.Warn("resolve telegram attachment: getFile failed",
			zap.String("telegram_account_id", account.ID.String()), zap.Error(err))
		return entity.MessageTypeText, nil
	}
	return msgType, &url
}

// previewFor mirrors instagram.previewFor exactly (same bracketed-label
// convention for conversations.last_message_preview) — not imported from
// that package for the same "usecases don't depend on each other" reason
// as EventPublisher/dmReceivedEvent above.
func previewFor(msg *entity.Message) string {
	if msg.Content != nil && *msg.Content != "" {
		preview := *msg.Content
		if len(preview) > 140 {
			preview = preview[:140]
		}
		return preview
	}
	switch msg.MessageType {
	case entity.MessageTypeImage:
		return "[Image]"
	case entity.MessageTypeVideo:
		return "[Video]"
	case entity.MessageTypeAudio:
		return "[Voice message]"
	case entity.MessageTypeFile:
		return "[File]"
	default:
		return ""
	}
}
