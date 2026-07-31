package instagram

import (
	"context"
	"encoding/json"
	"time"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/signature"
)

// EventPublisher is the narrow port this usecase needs onto the async
// event backbone — publish one JSON payload under a routing key. The
// concrete adapter is internal/platform/queue.Publisher (RabbitMQ).
type EventPublisher interface {
	Publish(ctx context.Context, routingKey string, payload any) error
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
	appSecret   string
	verifyToken string
}

func NewWebhookUseCase(
	logRepo repository.WebhookLogRepository,
	accountRepo repository.InstagramAccountRepository,
	convRepo repository.ConversationRepository,
	messageRepo repository.MessageRepository,
	publisher EventPublisher,
	appSecret string,
	verifyToken string,
) *WebhookUseCase {
	return &WebhookUseCase{
		logRepo:     logRepo,
		accountRepo: accountRepo,
		convRepo:    convRepo,
		messageRepo: messageRepo,
		publisher:   publisher,
		appSecret:   appSecret,
		verifyToken: verifyToken,
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
	MID  string `json:"mid"`
	Text string `json:"text"`
	// IsEcho is true when this notification is Meta echoing back a message
	// OUR side sent (via this API, or a human typing directly in the
	// Instagram app) — not a new customer message. Skipped in ingestMessage
	// below; without this check, the AI's/agent's own replies would be
	// re-ingested as if a customer sent them, corrupting the conversation
	// and potentially re-triggering an AI response to its own reply.
	IsEcho bool `json:"is_echo,omitempty"`
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
	}

	if existing, err := uc.messageRepo.FindByIGMessageID(ctx, account.OrganizationID, m.Message.MID); err == nil && existing != nil {
		return nil // already ingested — Meta redelivered; idempotent no-op
	}

	content := m.Message.Text
	createdAt := time.UnixMilli(m.Timestamp)
	msg := &entity.Message{
		OrganizationID: account.OrganizationID,
		ConversationID: conv.ID,
		Direction:      entity.MessageDirectionInbound,
		SenderType:     entity.MessageSenderCustomer,
		MessageType:    entity.MessageTypeText,
		Content:        &content,
		IGMessageID:    &m.Message.MID,
		CreatedAt:      createdAt,
	}
	if err := uc.messageRepo.Create(ctx, msg); err != nil {
		return err
	}

	preview := content
	if len(preview) > 140 {
		preview = preview[:140]
	}
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
