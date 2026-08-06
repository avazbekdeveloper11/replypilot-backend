package conversation

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/integration/geminiapi"
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

// TelegramSender mirrors internal/usecase/ai's — see that package's doc
// comment for why this isn't a shared type.
type TelegramSender interface {
	SendMessage(ctx context.Context, botToken, businessConnectionID, chatID, text string) error
}

// TelegramAccountLookup mirrors internal/usecase/ai's.
type TelegramAccountLookup interface {
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TelegramAccount, error)
}

// authError mirrors internal/usecase/ai's — see that package's doc comment
// for why this isn't a shared type.
type authError interface {
	IsAuthError() bool
	IsExpired() bool
}

// telegramAuthError mirrors internal/usecase/ai's.
type telegramAuthError interface {
	IsAuthError() bool
}

// Generator mirrors internal/usecase/ai's Generator port — used here only
// by Summarize, for on-demand conversation summarization, not for
// generating a reply to send. Declared separately per this package's
// established "usecases don't depend on each other" convention (see
// Sender's doc comment above for the fuller reasoning).
type Generator interface {
	Generate(ctx context.Context, systemPrompt, userMessage string) (string, geminiapi.GenerateUsage, error)
}

// maxSendMessageLen bounds SendMessage's Content — Instagram's Send API
// itself rejects overlong messages; failing fast with a clear error here
// beats a confusing 400 surfaced from Meta's API partway through.
const maxSendMessageLen = 1000

type UseCase struct {
	convRepo         repository.ConversationRepository
	msgRepo          repository.MessageRepository
	accountRepo      repository.InstagramAccountRepository
	sender           Sender
	encryptor        *crypto.AESGCMEncryptor
	telegramAccounts TelegramAccountLookup
	telegramSender   TelegramSender
	generator        Generator
}

func New(
	convRepo repository.ConversationRepository,
	msgRepo repository.MessageRepository,
	accountRepo repository.InstagramAccountRepository,
	sender Sender,
	encryptor *crypto.AESGCMEncryptor,
	telegramAccounts TelegramAccountLookup,
	telegramSender TelegramSender,
	generator Generator,
) *UseCase {
	return &UseCase{
		convRepo:         convRepo,
		msgRepo:          msgRepo,
		accountRepo:      accountRepo,
		sender:           sender,
		encryptor:        encryptor,
		telegramAccounts: telegramAccounts,
		telegramSender:   telegramSender,
		generator:        generator,
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

	// See internal/usecase/ai.HandleInboundMessage's identical channel
	// switch for why this branches on conv.Channel instead of always
	// assuming Instagram.
	switch conv.Channel {
	case entity.ConversationChannelTelegram:
		if err := uc.sendTelegramMessage(ctx, orgID, conv, content); err != nil {
			return nil, err
		}
	default:
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

// sendTelegramMessage mirrors internal/usecase/ai.UseCase.sendTelegramReply
// — see that method's doc comment for the full reasoning, not repeated
// here.
func (uc *UseCase) sendTelegramMessage(ctx context.Context, orgID uuid.UUID, conv *entity.Conversation, content string) error {
	if conv.TelegramAccountID == nil {
		return apperror.Internal("send telegram message", errors.New("conversation has channel=telegram but no telegram_account_id"))
	}
	account, err := uc.telegramAccounts.FindByID(ctx, orgID, *conv.TelegramAccountID)
	if err != nil {
		return err
	}
	if account.BusinessConnectionID == nil {
		return apperror.Internal("send telegram message", errors.New("telegram account has no business_connection_id"))
	}

	botToken, err := uc.encryptor.Decrypt(account.BotTokenEncrypted)
	if err != nil {
		return apperror.Internal("decrypt telegram bot token", err)
	}

	if err := uc.telegramSender.SendMessage(ctx, botToken, *account.BusinessConnectionID, conv.CustomerIGID, content); err != nil {
		uc.handleTelegramSendFailure(ctx, account, err)
		return apperror.Internal("send telegram message", err)
	}
	return nil
}

// handleTelegramSendFailure mirrors internal/usecase/ai.UseCase's method of
// the same name — see its doc comment for the full reasoning, including why
// this type-asserts uc.telegramAccounts to reach Update.
func (uc *UseCase) handleTelegramSendFailure(ctx context.Context, account *entity.TelegramAccount, sendErr error) {
	var ae telegramAuthError
	if !errors.As(sendErr, &ae) || !ae.IsAuthError() {
		return
	}
	if account.Status == entity.TelegramAccountStatusError {
		return
	}

	updater, ok := uc.telegramAccounts.(interface {
		Update(ctx context.Context, account *entity.TelegramAccount) error
	})
	if !ok {
		return
	}
	account.Status = entity.TelegramAccountStatusError
	_ = updater.Update(ctx, account)
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

// summaryMessageLimit bounds how much history Summarize reads — same
// "MVP-sized shop" capping convention as internal/usecase/ai's
// historyLimit/retrievalLimit, just a much larger window since a summary
// needs to see far more of a conversation than a single reply's rolling
// context does. 200 turns is generous for the kind of DM sales
// conversation this product handles; a conversation longer than that gets
// summarized from its most recent 200 messages, not its very first ones —
// a reasonable trade-off for an on-demand admin tool, not something a
// customer-facing feature depends on.
const summaryMessageLimit = 200

// summarySystemPrompt drives Summarize — deliberately separate from
// internal/usecase/ai's systemPromptTemplate (that one generates a reply
// TO the customer; this one generates a note ABOUT the conversation, FOR
// the admin, and is never sent to anyone outside the dashboard).
const summarySystemPrompt = `Siz ReplyPilot dasturidagi ichki yordamchisiz. Quyida bitta mijoz bilan bo'lgan yozishmalar transkripti berilgan (mijoz, AI va inson agent xabarlari). Vazifangiz — ushbu suhbatni administratorga tushunarli, qisqa xulosa qilib berish.

Quyidagilarni yoriting:
- Mijoz nima haqida yozgan, nimaga qiziqqan yoki nimani so'ragan
- Suhbat qanday yakunlangan yoki hozirda qanday holatda (masalan: sotib oldi, savol berdi-javob olmadi, shikoyat qildi, hali davom etyapti)
- Agar tegishli bo'lsa, keyingi qadam bo'yicha tavsiya

Faqat transkriptdagi ma'lumotga tayaning, hech narsa o'ylab topmang. Javobni oddiy matn shaklida yozing — markdown formatlash (**, *, # va h.k.) ishlatmang. 3-6 gapdan iborat qisqa xulosa yetarli.`

// Summarize generates (or regenerates — always overwrites, see
// entity.Conversation.AISummary's doc comment) an AI summary of what this
// customer and the business actually discussed, for the admin's benefit —
// the per-conversation counterpart to internal/usecase/insights' org-wide
// summary. On-demand only: nothing calls this automatically, so it never
// adds Gemini cost to the reply pipeline itself.
func (uc *UseCase) Summarize(ctx context.Context, orgID, id uuid.UUID) (*entity.Conversation, error) {
	conv, err := uc.convRepo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}

	history, err := uc.msgRepo.List(ctx, repository.MessageListParams{
		OrganizationID: orgID,
		ConversationID: id,
		Limit:          summaryMessageLimit,
	})
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, apperror.InvalidInput("conversation has no messages to summarize yet", nil)
	}

	transcript := buildSummaryTranscript(history)
	summary, _, err := uc.generator.Generate(ctx, summarySystemPrompt, transcript)
	if err != nil {
		return nil, apperror.Internal("generate conversation summary", err)
	}
	summary = strings.TrimSpace(stripSummaryMarkdownEmphasis(summary))
	if summary == "" {
		return nil, apperror.Internal("generate conversation summary", errors.New("gemini returned an empty summary"))
	}

	now := time.Now()
	conv.AISummary = &summary
	conv.AISummaryGeneratedAt = &now
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// buildSummaryTranscript mirrors internal/usecase/ai's buildTranscript
// (same newest-first-in, oldest-first-out reversal, same media-placeholder
// approach for a captionless attachment) but labels each turn by its
// actual sender_type rather than a flat "Customer"/"You" — Summarize's
// whole point is telling the admin whether the AI, a human teammate, or
// nobody actually handled the customer, so collapsing that distinction
// here would defeat the feature.
func buildSummaryTranscript(history []*entity.Message) string {
	var b strings.Builder
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		speaker := "Mijoz"
		if m.Direction == entity.MessageDirectionOutbound {
			switch m.SenderType {
			case entity.MessageSenderHuman:
				speaker = "Jamoa"
			case entity.MessageSenderSystem:
				speaker = "Tizim"
			default:
				speaker = "AI"
			}
		}
		switch {
		case m.Content != nil && strings.TrimSpace(*m.Content) != "":
			fmt.Fprintf(&b, "%s: %s\n", speaker, *m.Content)
		case m.MessageType == entity.MessageTypeImage:
			fmt.Fprintf(&b, "%s: [rasm yubordi]\n", speaker)
		case m.MessageType == entity.MessageTypeAudio:
			fmt.Fprintf(&b, "%s: [ovozli xabar yubordi]\n", speaker)
		case m.MessageType == entity.MessageTypeVideo:
			fmt.Fprintf(&b, "%s: [video yubordi]\n", speaker)
		}
	}
	return b.String()
}

// summaryMarkdownEmphasisRegex / stripSummaryMarkdownEmphasis mirror
// internal/usecase/ai's markdownEmphasisRegex/stripMarkdownEmphasis exactly
// — same backstop against Gemini emitting **bold**/*italic* despite being
// told not to, just applied to a summary shown in the dashboard rather
// than a reply sent to a customer. Duplicated, not shared, for the same
// "usecases don't depend on each other" reason as everything else in this
// file.
var summaryMarkdownEmphasisRegex = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__|\*(.+?)\*|_(.+?)_`)

func stripSummaryMarkdownEmphasis(text string) string {
	return summaryMarkdownEmphasisRegex.ReplaceAllStringFunc(text, func(match string) string {
		groups := summaryMarkdownEmphasisRegex.FindStringSubmatch(match)
		for _, g := range groups[1:] {
			if g != "" {
				return g
			}
		}
		return match
	})
}
