package instagram

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
	"github.com/replypilot/backend/pkg/signature"
)

// EventPublisher is the narrow port this usecase needs onto the async
// event backbone — publish one JSON payload under a routing key. The
// concrete adapter is internal/platform/queue.Publisher (RabbitMQ).
type EventPublisher interface {
	Publish(ctx context.Context, routingKey string, payload any) error
}

// ProfileFetcher is the narrow port onto Meta's Graph API needed to
// resolve a customer's Instagram username from their IGSID (the sender
// ID a webhook delivery carries — Meta's messaging payload has no
// username field, only that opaque ID). Satisfied by
// internal/integration/metaapi.Client.FetchCustomerUsername — deliberately
// NOT the same method oauth_usecase.go uses (FetchProfile): that one
// requests a `user_id` field that Meta's Graph API only allows when the
// node being queried is the connecting business's own account, and
// rejects outright for an arbitrary customer's IGSID. See
// FetchCustomerUsername's doc comment in internal/integration/metaapi for
// how that was discovered.
type ProfileFetcher interface {
	FetchCustomerUsername(ctx context.Context, accessToken, igUserID string) (username string, err error)
}

// RoutingKeyDMReceived is the event a downstream AI-processing worker
// (cmd/worker-ai in the full system — not part of this API service)
// consumes to pick up the next step of the pipeline described in
// docs/ARCHITECTURE.md §6. This service's job ends at "persisted and
// published"; RAG retrieval, grounded generation, and the confidence gate
// live in that worker, not here.
const RoutingKeyDMReceived = "dm.received"

type DMReceivedEvent struct {
	OrganizationID     string `json:"organization_id"`
	ConversationID     string `json:"conversation_id"`
	MessageID          string `json:"message_id"`
	InstagramAccountID string `json:"instagram_account_id"`
}

type WebhookUseCase struct {
	logRepo     repository.WebhookLogRepository
	accountRepo repository.InstagramAccountRepository
	convRepo    repository.ConversationRepository
	messageRepo repository.MessageRepository
	publisher   EventPublisher
	profiles    ProfileFetcher
	encryptor   *crypto.AESGCMEncryptor
	appSecret   string
	verifyToken string
	logger      *zap.Logger
}

func NewWebhookUseCase(
	logRepo repository.WebhookLogRepository,
	accountRepo repository.InstagramAccountRepository,
	convRepo repository.ConversationRepository,
	messageRepo repository.MessageRepository,
	publisher EventPublisher,
	profiles ProfileFetcher,
	encryptor *crypto.AESGCMEncryptor,
	appSecret string,
	verifyToken string,
	logger *zap.Logger,
) *WebhookUseCase {
	return &WebhookUseCase{
		logRepo:     logRepo,
		accountRepo: accountRepo,
		convRepo:    convRepo,
		messageRepo: messageRepo,
		publisher:   publisher,
		profiles:    profiles,
		encryptor:   encryptor,
		appSecret:   appSecret,
		verifyToken: verifyToken,
		logger:      logger,
	}
}

// VerifySubscription answers Meta's webhook handshake: a GET request
// carrying hub.mode=subscribe, hub.verify_token, and hub.challenge.
// Returning the challenge value unmodified is what proves to Meta this
// endpoint is the one you registered, not an impostor.
func (uc *WebhookUseCase) VerifySubscription(mode, token, challenge string) (string, error) {
	if mode != "subscribe" || token != uc.verifyToken {
		return "", apperror.Unauthorized("webhook verification failed")
	}
	return challenge, nil
}

// --- payload shapes matching Meta's Instagram Messaging webhook format ---
// https://developers.facebook.com/docs/messenger-platform/instagram/features/webhook

type webhookPayload struct {
	Object string         `json:"object"`
	Entry  []webhookEntry `json:"entry"`
}

type webhookEntry struct {
	ID        string             `json:"id"` // the Instagram business account this delivery is for
	Time      int64              `json:"time"`
	Messaging []webhookMessaging `json:"messaging"`
}

type webhookMessaging struct {
	Sender    webhookParty    `json:"sender"`
	Recipient webhookParty    `json:"recipient"`
	Timestamp int64           `json:"timestamp"`
	Message   *webhookMessage `json:"message,omitempty"`
}

type webhookParty struct {
	ID string `json:"id"`
}

type webhookMessage struct {
	MID string `json:"mid"`
	Text string `json:"text"`
	// Attachments carries image/video/audio/file payloads — previously
	// unmarshaled into nothing (Go's json package silently drops fields a
	// struct doesn't declare), so every non-text DM was persisted as an
	// empty-content text message and the AI pipeline had nothing to react
	// to. Meta only ever sends one attachment per message in practice, but
	// the field is an array per Meta's own schema, so this mirrors that
	// rather than assuming a single element.
	Attachments []webhookAttachment `json:"attachments,omitempty"`
	// IsEcho is true when this notification is Meta echoing back a message
	// OUR side sent (via this API, or a human typing directly in the
	// Instagram app) — not a new customer message. Skipped in ingestMessage
	// below; without this check, the AI's/agent's own replies would be
	// re-ingested as if a customer sent them, corrupting the conversation
	// and potentially re-triggering an AI response to its own reply.
	IsEcho bool `json:"is_echo,omitempty"`
}

// webhookAttachment mirrors Meta's message.attachments[] shape:
// https://developers.facebook.com/docs/messenger-platform/instagram/features/webhook#messages
// Type is one of "image", "video", "audio", "file" (also "story_mention"
// for a story reply, "fallback" for some link-preview cases — anything
// this codebase doesn't specifically handle falls through to
// entity.MessageTypeFile in ingestMessage, which is a safe default: it's
// stored and visible in the conversation, just not something the AI
// pipeline actively reasons about yet).
type webhookAttachment struct {
	Type    string                   `json:"type"`
	Payload webhookAttachmentPayload `json:"payload"`
}

type webhookAttachmentPayload struct {
	// URL is a Meta-hosted CDN link. It is NOT guaranteed long-lived —
	// fetch it promptly (see internal/usecase/ai's image-download step)
	// rather than persisting it for later use days out.
	URL string `json:"url"`
}

// Process verifies the signature, records the delivery in webhook_logs
// regardless of outcome (see entity.WebhookLog doc comment — this is the
// audit trail for "did Meta actually send this"), and for a valid,
// recognized delivery persists the inbound message idempotently and
// publishes dm.received.
func (uc *WebhookUseCase) Process(ctx context.Context, signatureHeader string, rawBody []byte) error {
	sigErr := signature.VerifyMetaSignature(signatureHeader, uc.appSecret, rawBody)

	log := &entity.WebhookLog{
		Source:         entity.WebhookSourceMeta,
		Payload:        rawBody,
		SignatureValid: sigErr == nil,
		Status:         entity.WebhookStatusReceived,
	}
	if err := uc.logRepo.Create(ctx, log); err != nil {
		return err
	}

	if sigErr != nil {
		errMsg := sigErr.Error()
		_ = uc.logRepo.UpdateStatus(ctx, log.ID, entity.WebhookStatusFailed, &errMsg)
		return apperror.Unauthorized("invalid webhook signature")
	}

	var payload webhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		errMsg := err.Error()
		_ = uc.logRepo.UpdateStatus(ctx, log.ID, entity.WebhookStatusFailed, &errMsg)
		return apperror.InvalidInput("malformed webhook payload", err)
	}

	var processErr error
	for _, entry := range payload.Entry {
		for _, m := range entry.Messaging {
			if m.Message == nil || m.Message.MID == "" {
				continue // delivery/read receipts and similar — nothing to ingest
			}
			if m.Message.IsEcho {
				continue // our own outbound message, echoed back by Meta — not a customer message
			}
			if err := uc.ingestMessage(ctx, entry.ID, m); err != nil {
				processErr = err
			}
		}
	}

	if processErr != nil {
		errMsg := processErr.Error()
		_ = uc.logRepo.UpdateStatus(ctx, log.ID, entity.WebhookStatusFailed, &errMsg)
		return processErr
	}

	return uc.logRepo.UpdateStatus(ctx, log.ID, entity.WebhookStatusProcessed, nil)
}

func (uc *WebhookUseCase) ingestMessage(ctx context.Context, igBusinessAccountID string, m webhookMessaging) error {
	account, err := uc.accountRepo.FindByIGUserID(ctx, igBusinessAccountID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			// Not one of our tenants' accounts — ignore rather than error.
			// Legitimate for a deauthorized or orphaned subscription.
			return nil
		}
		return err
	}

	conv, err := uc.convRepo.FindByAccountAndCustomer(ctx, account.OrganizationID, account.ID, m.Sender.ID)
	isNewConversation := false
	if err != nil {
		if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
			return err
		}
		conv = &entity.Conversation{
			OrganizationID:     account.OrganizationID,
			InstagramAccountID: account.ID,
			CustomerIGID:       m.Sender.ID,
			Status:             entity.ConversationStatusAIActive,
		}
		if err := uc.convRepo.Create(ctx, conv); err != nil {
			return err
		}
		isNewConversation = true
	}

	// Resolve the customer's Instagram username so the inbox shows a name
	// instead of a bare numeric IGSID (m.Sender.ID) — the webhook payload
	// itself never carries one (see ProfileFetcher's doc comment). Best
	// effort: a lookup failure (rate limit, transient Graph API error)
	// must not block message ingestion, so it's logged-and-swallowed, not
	// returned. Runs on every new conversation, and once more per
	// pre-existing conversation that predates this field to backfill it —
	// after that first successful fetch CustomerUsername is set and this
	// branch is skipped for good.
	//
	// Both failure points are logged at Warn (not silently dropped) — this
	// used to swallow errors with no trace at all, which made "why is the
	// username never showing up" undiagnosable from logs alone. (The
	// per-message "entering block" / "succeeded" Info-level tracing that
	// was here during initial diagnosis has been removed now that the
	// root cause — FetchProfile requesting a field Graph API rejects for
	// this node — is confirmed and fixed via FetchCustomerUsername.)
	if (isNewConversation || conv.CustomerUsername == nil) && uc.profiles != nil {
		if accessToken, decErr := uc.encryptor.Decrypt(account.AccessTokenEncrypted); decErr != nil {
			uc.logger.Warn("resolve customer IG username: decrypt account access token failed",
				zap.String("instagram_account_id", account.ID.String()),
				zap.Error(decErr),
			)
		} else if username, fetchErr := uc.profiles.FetchCustomerUsername(ctx, accessToken, m.Sender.ID); fetchErr != nil {
			uc.logger.Warn("resolve customer IG username: FetchCustomerUsername failed",
				zap.String("instagram_account_id", account.ID.String()),
				zap.String("sender_igsid", m.Sender.ID),
				zap.Error(fetchErr),
			)
		} else if username != "" {
			conv.CustomerUsername = &username
		}
	}

	if existing, err := uc.messageRepo.FindByIGMessageID(ctx, account.OrganizationID, m.Message.MID); err == nil && existing != nil {
		return nil // already ingested — Meta redelivered; idempotent no-op
	}

	msgType, attachmentURL := classifyAttachment(m.Message.Attachments)
	createdAt := time.UnixMilli(m.Timestamp)
	msg := &entity.Message{
		OrganizationID: account.OrganizationID,
		ConversationID: conv.ID,
		Direction:      entity.MessageDirectionInbound,
		SenderType:     entity.MessageSenderCustomer,
		MessageType:    msgType,
		AttachmentURL:  attachmentURL,
		IGMessageID:    &m.Message.MID,
		CreatedAt:      createdAt,
	}
	// Content stays nil (not a pointer to "") when there's no caption text —
	// internal/usecase/ai's empty-content gate and buildTranscript both key
	// off nil to distinguish "no text" from "text that happens to be
	// empty," and a non-nil-but-empty Content would defeat that.
	if m.Message.Text != "" {
		text := m.Message.Text
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

	return uc.publisher.Publish(ctx, RoutingKeyDMReceived, DMReceivedEvent{
		OrganizationID:     account.OrganizationID.String(),
		ConversationID:     conv.ID.String(),
		MessageID:          msg.ID.String(),
		InstagramAccountID: account.ID.String(),
	})
}

// classifyAttachment inspects the first attachment (see webhookAttachment's
// doc comment on why only the first) and returns the entity.MessageType to
// store plus the attachment's URL, or (MessageTypeText, nil) when there is
// none — a plain text-only message.
func classifyAttachment(attachments []webhookAttachment) (entity.MessageType, *string) {
	if len(attachments) == 0 {
		return entity.MessageTypeText, nil
	}
	att := attachments[0]
	if att.Payload.URL == "" {
		return entity.MessageTypeText, nil
	}
	url := att.Payload.URL
	switch att.Type {
	case "image":
		return entity.MessageTypeImage, &url
	case "video":
		return entity.MessageTypeVideo, &url
	case "audio":
		return entity.MessageTypeAudio, &url
	default:
		return entity.MessageTypeFile, &url
	}
}

// previewFor renders conversations.last_message_preview for the inbox list
// view. Text messages preview their own content (unchanged behavior); an
// attachment with no caption text previously had nothing to show here (the
// preview was just always `m.Message.Text`, empty for every non-text
// message) — a short bracketed label is a small but real UX fix on its
// own, independent of whether the AI understands the attachment yet.
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
