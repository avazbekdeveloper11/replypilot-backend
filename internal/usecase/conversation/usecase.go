package conversation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// Sender is the port onto Meta's Send API — satisfied by
// internal/integration/metaapi.Client.SendMessage. Same shape as
// internal/usecase/ai.Sender, deliberately not shared/imported from there:
// usecases in this codebase don't depend on each other (see
// internal/usecase/ai's authError doc comment for the same reasoning) —
// each package declares the narrow port it needs against the same
// concrete client.
type Sender interface {
	SendMessage(ctx context.Context, accessToken, recipientIGID, text string) error
}

// authError mirrors internal/usecase/ai's — see that package's doc comment
// for why this isn't a shared type.
type authError interface {
	IsAuthError() bool
	IsExpired() bool
}

// maxSendMessageLen bounds SendMessage's Content — Instagram's Send API
// itself rejects overlong messages; failing fast with a clear error here
// beats a confusing 400 surfaced from Meta's API partway through.
const maxSendMessageLen = 1000

type UseCase struct {
	convRepo    repository.ConversationRepository
	msgRepo     repository.MessageRepository
	accountRepo repository.InstagramAccountRepository
	sender      Sender
	encryptor   *crypto.AESGCMEncryptor
}

func New(
	convRepo repository.ConversationRepository,
	msgRepo repository.MessageRepository,
	accountRepo repository.InstagramAccountRepository,
	sender Sender,
	encryptor *crypto.AESGCMEncryptor,
) *UseCase {
	return &UseCase{
		convRepo:    convRepo,
		msgRepo:     msgRepo,
		accountRepo: accountRepo,
		sender:      sender,
		encryptor:   encryptor,
	}
}

func (uc *UseCase) List(ctx context.Context, params repository.ConversationListParams) ([]*entity.Conversation, error) {
	return uc.convRepo.List(ctx, params)
}

func (uc *UseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*entity.Conversation, error) {
	return uc.convRepo.FindByID(ctx, orgID, id)
}

// TakeOver moves a conversation to human_active, either because the AI
// pipeline handed it off (pending_human — see internal/usecase/ai's
// confidence-gate doc comment for how a conversation gets there) or
// because an admin voluntarily decides to step in on a thread the AI is
// still actively handling (ai_active). The two starting points read the
// same from here on: AssignedUserID is set, and — critically —
// internal/usecase/ai.HandleInboundMessage's very first check is
// `conv.Status != ConversationStatusAIActive`, so the instant this call
// succeeds, the AI stops replying on this thread no matter which status it
// came from.
//
// Still deliberately NOT allowed from human_active (already taken over —
// re-taking over doesn't mean anything, and would silently reassign
// AssignedUserID to whoever clicks the button, which is a landmine for a
// multi-agent team) or resolved/closed (over, nothing to take over).
func (uc *UseCase) TakeOver(ctx context.Context, orgID, id, userID uuid.UUID) (*entity.Conversation, error) {
	conv, err := uc.convRepo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if conv.Status != entity.ConversationStatusPendingHuman && conv.Status != entity.ConversationStatusAIActive {
		return nil, apperror.InvalidInput("only a conversation the AI is still handling, or one pending human handoff, can be taken over", nil)
	}

	conv.Status = entity.ConversationStatusHumanActive
	conv.AssignedUserID = &userID
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// SendMessage is a human agent's reply, sent from the dashboard once
// they've taken over a conversation (see TakeOver) — the send-side
// counterpart to internal/usecase/ai.HandleInboundMessage's AI-generated
// replies, structured the same way (decrypt the account's token, call
// Meta's Send API, persist the outbound message, update the conversation's
// list-view fields) but attributed to the human who sent it instead of the
// AI.
//
// Deliberately restricted to human_active: sending only makes sense once
// someone has actually taken over (see TakeOver's doc comment on why
// ai_active and pending_human both route through it first) — allowing a
// send from ai_active would let a human and the AI reply in the same
// thread without either knowing about the other's message, which is
// exactly the confusion the handoff state machine exists to prevent.
func (uc *UseCase) SendMessage(ctx context.Context, orgID, id, userID uuid.UUID, content string) (*entity.Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, apperror.InvalidInput("message content is required", nil)
	}
	if len(content) > maxSendMessageLen {
		return nil, apperror.InvalidInput("message is too long", nil)
	}

	conv, err := uc.convRepo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if conv.Status != entity.ConversationStatusHumanActive {
		return nil, apperror.InvalidInput("only a conversation you've taken over can be replied to — take over first", nil)
	}

	account, err := uc.accountRepo.FindByID(ctx, orgID, conv.InstagramAccountID)
	if err != nil {
		return nil, err
	}
	accessToken, err := uc.encryptor.Decrypt(account.AccessTokenEncrypted)
	if err != nil {
		return nil, apperror.Internal("decrypt instagram access token", err)
	}

	if err := uc.sender.SendMessage(ctx, accessToken, conv.CustomerIGID, content); err != nil {
		uc.handleSendFailure(ctx, account, err)
		return nil, apperror.Internal("send instagram reply", err)
	}

	outbound := &entity.Message{
		OrganizationID: orgID,
		ConversationID: conv.ID,
		Direction:      entity.MessageDirectionOutbound,
		SenderType:     entity.MessageSenderHuman,
		SenderUserID:   &userID,
		MessageType:    entity.MessageTypeText,
		Content:        &content,
	}
	if err := uc.msgRepo.Create(ctx, outbound); err != nil {
		// The message already reached the customer over Instagram — see
		// internal/usecase/ai.HandleInboundMessage's identical comment on
		// its own outbound-message Create call for why this still returns
		// an error rather than silently succeeding: the caller needs to
		// know the local record is out of sync with what was actually sent.
		return nil, apperror.Internal("persist outbound message", err)
	}

	preview := content
	if len(preview) > maxReplyPreviewLen {
		preview = preview[:maxReplyPreviewLen]
	}
	now := time.Now()
	conv.LastMessageAt = &now
	conv.LastMessagePreview = &preview
	conv.UnreadCount = 0
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return nil, apperror.Internal("update conversation after send", err)
	}

	return outbound, nil
}

// maxReplyPreviewLen mirrors internal/usecase/ai's constant of the same
// name and purpose — conversations.last_message_preview is a list-view
// teaser, not the full message, for either sender.
const maxReplyPreviewLen = 140

// handleSendFailure mirrors internal/usecase/ai.UseCase's method of the
// same name — see its doc comment for the full reasoning (Meta auth-error
// code 190 detection, why this swallows its own error). Duplicated rather
// than shared for the same reason Sender/authError above are: this
// package doesn't import internal/usecase/ai.
func (uc *UseCase) handleSendFailure(ctx context.Context, account *entity.InstagramAccount, sendErr error) {
	var ae authError
	if !errors.As(sendErr, &ae) || !ae.IsAuthError() {
		return
	}

	newStatus := entity.InstagramAccountStatusRevoked
	if ae.IsExpired() {
		newStatus = entity.InstagramAccountStatusExpired
	}
	if account.Status == newStatus {
		return
	}

	account.Status = newStatus
	_ = uc.accountRepo.Update(ctx, account)
}

// Resolve is the other half of the handoff state machine TakeOver starts —
// see entity.Conversation's doc comment on the AI_ACTIVE -> PENDING_HUMAN
// -> HUMAN_ACTIVE -> RESOLVED chain. Before this method existed, nothing
// anywhere ever set status=resolved, so the "Resolved" filter tab on the
// Conversations page could never show anything — not a frontend bug, the
// backend simply had no way to reach that state.
//
// Allowed from human_active (the normal path: a human wraps up after
// TakeOver) or pending_human directly (a human looks at something the AI
// flagged and decides it doesn't need a reply at all — e.g. spam, or a
// question that answered itself). Not allowed from ai_active: resolving a
// conversation the AI still owns would silently stop it from ever
// replying again on that thread with no human having actually looked at
// it, which is a worse failure mode than just leaving it ai_active.
func (uc *UseCase) Resolve(ctx context.Context, orgID, id uuid.UUID) (*entity.Conversation, error) {
	conv, err := uc.convRepo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if conv.Status != entity.ConversationStatusHumanActive && conv.Status != entity.ConversationStatusPendingHuman {
		return nil, apperror.InvalidInput("only a conversation being handled by a human can be marked resolved", nil)
	}

	conv.Status = entity.ConversationStatusResolved
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// ListMessages checks the conversation belongs to orgID before returning
// any messages. This is a deliberate second check on top of row-level
// security, not redundant with it: RLS protects against a missing WHERE
// clause inside a repository method, but a caller who passes a
// conversation_id belonging to a *different, valid* org context could
// still get past a naive "just query messages by conversation_id" — this
// FindByID call is what turns that into a 404 instead of a data leak.
func (uc *UseCase) ListMessages(ctx context.Context, orgID, conversationID uuid.UUID, cursorBefore *time.Time, limit int) ([]*entity.Message, error) {
	if _, err := uc.convRepo.FindByID(ctx, orgID, conversationID); err != nil {
		return nil, err
	}

	return uc.msgRepo.List(ctx, repository.MessageListParams{
		OrganizationID: orgID,
		ConversationID: conversationID,
		CursorBefore:   cursorBefore,
		Limit:          limit,
	})
}
