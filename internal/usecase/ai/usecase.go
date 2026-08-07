// Package ai is the AI reply pipeline's core logic: given an inbound
// customer message, retrieve grounding context from the org's knowledge
// base (RAG), generate a reply with Gemini, send it back to the customer
// via Instagram, and record what happened. Every inbound message gets a
// reply — see CONFIDENCE below for what changed and why.
//
// This is the "downstream AI-processing worker" internal/usecase/instagram's
// webhook_usecase.go doc comment describes as living outside the API
// service — cmd/worker-ai is that worker; this package is its usecase
// layer, wired the same constructor-injection way as every other usecase
// in this codebase.
//
// CONFIDENCE — READ BEFORE CHANGING THE THRESHOLD
//
//	Gemini's generateContent API does not return a confidence score for a
//	generation. What this usecase calls "confidence" is a heuristic proxy:
//	the top RAG-retrieved chunk's cosine similarity to the customer's
//	message. It used to gate whether Gemini was called at all — below
//	confidenceThreshold, the usecase handed the conversation to a human
//	WITHOUT generating a reply. That produced no customer-visible response
//	whatsoever for anything that wasn't a close textual match to the
//	knowledge base — including plain "salom" — which is indistinguishable
//	from the bot being broken and is not acceptable for a DM automation
//	product ("AI mijozlarni hamma yozgan DMlariga javob berishi kerak").
//	confidence is still computed and still stored on ai_responses (see
//	HandleInboundMessage), but it no longer blocks generation. The
//	anti-hallucination guardrail now lives entirely in
//	systemPromptTemplate's instructions — answer only from context, and say
//	so and offer a human follow-up rather than invent facts. Gemini decides
//	per-message whether it has enough grounding for specifics; this usecase
//	no longer makes that call up front from one cosine-similarity number.
//
// HANDOFF AND ai_responses
//
//	ai_responses.message_id is a NOT NULL composite FK into the partitioned
//	messages table (see migrations/000001's trade-off note) — a row here
//	can only exist for an actual sent message. handoff (see that method) is
//	now reached only when Gemini itself fails or returns an empty
//	completion — a true "could not produce anything to send" case, not a
//	confidence judgment — so it stays rare, and ai_responses.was_handoff_triggered
//	is repurposed to flag "replied below the grounding threshold" instead of
//	"no reply was attempted." See HandleInboundMessage's WasHandoffTriggered
//	assignment.
package ai

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
	"github.com/replypilot/backend/internal/integration/clickapi"
	"github.com/replypilot/backend/internal/integration/geminiapi"
	"github.com/replypilot/backend/pkg/crypto"
)

const (
	retrievalLimit = 5
	historyLimit   = 10

	// confidenceThreshold is a starting point, not a measured value — tune
	// once real conversation/citation data exists to compare against actual
	// human handoff/correction rates. See the package doc comment.
	confidenceThreshold = 0.55

	// maxReplyPreviewLen mirrors the 140-char preview truncation already
	// used in instagram.WebhookUseCase.ingestMessage, for the same reason:
	// conversations.last_message_preview is a list-view teaser, not the
	// full message.
	maxReplyPreviewLen = 140
)

// Generator is the narrow port onto Gemini's text generation this usecase
// needs — satisfied by internal/integration/geminiapi.Client.Generate.
type Generator interface {
	Generate(ctx context.Context, systemPrompt, userMessage string) (string, geminiapi.GenerateUsage, error)
}

// MediaGenerator is Generator's multimodal counterpart — satisfied by
// internal/integration/geminiapi.Client.GenerateWithMedia. Kept as its own
// narrow interface (rather than adding the method to Generator) so a fake
// Generator used in tests for the plain-text path doesn't also have to
// implement media support it never exercises. Covers both images and voice
// messages (see HandleInboundMessage's isMedia) — Gemini's request shape is
// identical for both, only the mimeType differs, so one interface/method
// serves both rather than ImageGenerator/AudioGenerator duplicating each
// other.
type MediaGenerator interface {
	GenerateWithMedia(ctx context.Context, systemPrompt, userMessage string, mediaData []byte, mediaMimeType string) (string, geminiapi.GenerateUsage, error)
}

// MediaFetcher is the narrow port onto downloading a customer-sent
// attachment's bytes — satisfied by internal/integration/metaapi.Client.DownloadAttachment.
// See HandleInboundMessage's media-handling branch for why this fetch
// happens here (at reply-generation time) rather than at webhook-ingestion
// time: the attachment URL is short-lived, and ingestion (cmd/api) is
// meant to stay fast/thin — the actual Gemini call already happens in this
// worker, so fetching the bytes right before that call keeps the "URL
// might be stale" window as small as possible.
//
// cmd/worker-ai wires the same metaapi.Client instance here for BOTH
// channels, not one per channel: DownloadAttachment's implementation is a
// plain, unauthenticated, size-capped GET with nothing Meta-specific about
// it, and a Telegram attachment's URL (built by
// telegramapi.Client.ResolveFileURL, called at Telegram webhook ingestion
// time) already has its bot token embedded in the path, so it's just as
// fetchable by a bare GET as Meta's pre-signed CDN links are. Reusing one
// client here is accurate, not a hack — see ResolveFileURL's doc comment
// for the other half of this.
type MediaFetcher interface {
	DownloadAttachment(ctx context.Context, url string) (data []byte, mimeType string, err error)
}

// Retriever is the RAG retrieval port — internal/usecase/knowledgebase.UseCase
// satisfies it via its own Search method.
type Retriever interface {
	Search(ctx context.Context, orgID uuid.UUID, query string, limit int) ([]repository.ChunkSearchResult, error)
}

// Sender is the port onto Meta's Send API — satisfied by
// internal/integration/metaapi.Client.SendMessage.
type Sender interface {
	SendMessage(ctx context.Context, accessToken, recipientIGID, text string) error
}

// PrivateReplySender is the port onto Meta's private-reply send —
// satisfied by internal/integration/metaapi.Client.SendPrivateReply.
// Separate from Sender because it's only ever correct for the FIRST
// message to someone who reached us by commenting: the message must be
// addressed to the comment id, and Meta allows exactly one per comment.
// See that method's doc comment, and instagram.MetadataKeyPrivateReplyCommentID
// for how HandleInboundMessage knows which case it's in.
type PrivateReplySender interface {
	SendPrivateReply(ctx context.Context, accessToken, commentID, text string) error
}

// metadataKeyPrivateReplyCommentID mirrors
// instagram.MetadataKeyPrivateReplyCommentID's value. Duplicated as an
// unexported const rather than imported for the same "usecases don't
// depend on each other" reason as Sender/TelegramSender being declared
// per-package — the two are a documented pair; changing one without the
// other breaks comment-to-DM silently, so both carry a cross-reference.
const metadataKeyPrivateReplyCommentID = "private_reply_comment_id"

// TelegramSender is Sender's Telegram-channel counterpart — satisfied by
// internal/integration/telegramapi.Client.SendMessage. Kept as its own
// interface rather than folded into Sender: the two APIs need different
// per-send arguments (a decrypted bot token + business_connection_id vs.
// an access token), so there's no shared signature to unify them under.
type TelegramSender interface {
	SendMessage(ctx context.Context, botToken, businessConnectionID, chatID, text string) error
}

// TelegramAccountLookup is the narrow port this usecase needs onto
// TelegramAccount — satisfied by repository.TelegramAccountRepository,
// which offers more methods than this interface asks for (same "narrow
// port over a fatter repository" pattern as ProductLister/ClickIntegrationLookup
// above).
type TelegramAccountLookup interface {
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TelegramAccount, error)
}

// ProductLister is the narrow port onto the org's price catalog — satisfied
// by repository.ProductRepository, which this usecase needs no other
// method from. See buildProductContext.
type ProductLister interface {
	ListActiveByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error)
}

// ClickIntegrationLookup is the narrow port onto whether/how the org has
// connected Click — satisfied by repository.ClickIntegrationRepository.
// FindByOrganization returning apperror.CodeNotFound is the expected,
// common case (most orgs haven't connected Click) — see
// buildProductContext, which treats that exactly like "no integration",
// not an error worth failing the whole reply over.
type ClickIntegrationLookup interface {
	FindByOrganization(ctx context.Context, orgID uuid.UUID) (*entity.ClickIntegration, error)
}

// Leads is the narrow port onto lead capture — satisfied by
// repository.LeadRepository. HasOpen is what stops a lead from being
// re-created on every single subsequent message once a phone number has
// already been captured and nobody's acted on it yet — see
// captureLeadIfPresent and entity.Lead's doc comment.
type Leads interface {
	Create(ctx context.Context, lead *entity.Lead) error
	HasOpen(ctx context.Context, orgID, conversationID uuid.UUID) (bool, error)
}

// authError is satisfied by a Sender error that can identify itself as a
// Meta authentication failure (an invalid, expired, or revoked access
// token) — metaapi.GraphAPIError implements this for Graph API error code
// 190. Declared here rather than importing metaapi directly, so this
// usecase's dependency on the Sender port stays a pure interface — a fake
// Sender used in tests can return any error type that implements this
// without pulling in the real HTTP client. See handleSendFailure.
type authError interface {
	IsAuthError() bool
	// IsExpired distinguishes "token ran past its ~60-day lifetime"
	// (recoverable by cmd/token-refresh) from any other 190 subcode
	// (app deauthorized, password changed — recoverable only by the user
	// reconnecting from scratch). Only meaningful when IsAuthError() is
	// true.
	IsExpired() bool
}

type UseCase struct {
	convRepo         repository.ConversationRepository
	msgRepo          repository.MessageRepository
	accountRepo      repository.InstagramAccountRepository
	aiRespRepo       repository.AIResponseRepository
	retriever        Retriever
	generator        Generator
	sender           Sender
	encryptor        *crypto.AESGCMEncryptor
	products         ProductLister
	click            ClickIntegrationLookup
	leads            Leads
	mediaGen         MediaGenerator
	media            MediaFetcher
	telegramAccounts TelegramAccountLookup
	telegramSender   TelegramSender
	privateReply     PrivateReplySender
}

func New(
	convRepo repository.ConversationRepository,
	msgRepo repository.MessageRepository,
	accountRepo repository.InstagramAccountRepository,
	aiRespRepo repository.AIResponseRepository,
	retriever Retriever,
	generator Generator,
	sender Sender,
	encryptor *crypto.AESGCMEncryptor,
	products ProductLister,
	click ClickIntegrationLookup,
	leads Leads,
	mediaGen MediaGenerator,
	media MediaFetcher,
	telegramAccounts TelegramAccountLookup,
	telegramSender TelegramSender,
	privateReply PrivateReplySender,
) *UseCase {
	return &UseCase{
		convRepo:         convRepo,
		msgRepo:          msgRepo,
		accountRepo:      accountRepo,
		aiRespRepo:       aiRespRepo,
		retriever:        retriever,
		generator:        generator,
		sender:           sender,
		encryptor:        encryptor,
		products:         products,
		click:            click,
		leads:            leads,
		mediaGen:         mediaGen,
		media:            media,
		telegramAccounts: telegramAccounts,
		telegramSender:   telegramSender,
		privateReply:     privateReply,
	}
}

// InboundEvent mirrors instagram.DMReceivedEvent's fields — this package
// deliberately does not import internal/usecase/instagram (usecases don't
// depend on each other), so cmd/worker-ai unmarshals the queue payload into
// its own copy of that shape and maps it into this struct at the boundary.
type InboundEvent struct {
	OrganizationID     uuid.UUID
	ConversationID     uuid.UUID
	MessageID          uuid.UUID
	InstagramAccountID uuid.UUID
}

// HandleInboundMessage is the pipeline's entry point, called once per
// dm.received event. It is safe to call more than once for the same event
// (e.g. after a requeue) in the sense that it won't corrupt data — worst
// case a low-confidence message gets re-evaluated, or (rarely, if a prior
// attempt sent the reply but crashed before returning) a duplicate reply
// gets sent. True end-to-end idempotency would need an
// already-responded-to check keyed on MessageID before sending; not added
// here — see the package doc comment's honesty about what's NOT covered.
func (uc *UseCase) HandleInboundMessage(ctx context.Context, ev InboundEvent) error {
	conv, err := uc.convRepo.FindByID(ctx, ev.OrganizationID, ev.ConversationID)
	if err != nil {
		return err
	}

	// Only respond while the AI owns the conversation — once a human has
	// taken over, or it's pending/resolved/closed, the AI must not
	// re-engage. See entity.Conversation's doc comment on the handoff state
	// machine.
	if conv.Status != entity.ConversationStatusAIActive {
		return nil
	}

	history, err := uc.msgRepo.List(ctx, repository.MessageListParams{
		OrganizationID: ev.OrganizationID,
		ConversationID: ev.ConversationID,
		Limit:          historyLimit,
	})
	if err != nil {
		return err
	}

	latest := findMessage(history, ev.MessageID)
	hasText := latest != nil && latest.Content != nil && strings.TrimSpace(*latest.Content) != ""
	// isMedia covers images, voice messages, and videos — all three go
	// through the same Gemini multimodal path (see MediaGenerator's doc
	// comment); file attachments are deliberately excluded, since a
	// generic "file" has no defined content shape for Gemini to reason
	// about the way it can with these three. Additionally requires
	// mediaGen/media to be wired — if either is nil (e.g. a test double
	// that only fakes the text path), this falls through to hasText-only
	// handling below instead of panicking on a nil interface call.
	isMedia := latest != nil &&
		(latest.MessageType == entity.MessageTypeImage ||
			latest.MessageType == entity.MessageTypeAudio ||
			latest.MessageType == entity.MessageTypeVideo) &&
		latest.AttachmentURL != nil && uc.mediaGen != nil && uc.media != nil
	if !hasText && !isMedia {
		// Nothing to respond to — e.g. a file attachment with no caption,
		// or (shouldn't happen, but fail safe) the event's message isn't in
		// the fetched history window.
		return nil
	}

	// RAG retrieval needs real text to embed and search against. A
	// media-only message (no caption) has none — hits stays empty and
	// buildSystemPrompt falls back to its "(no context available)"
	// placeholder, same as any other ungrounded turn. Once there IS text
	// (a caption, or this is a text message), search on it as before.
	var hits []repository.ChunkSearchResult
	if hasText {
		hits, err = uc.retriever.Search(ctx, ev.OrganizationID, *latest.Content, retrievalLimit)
		if err != nil {
			return err
		}
	}

	// confidence is still computed and still recorded on the ai_responses row
	// (see below) for reporting, but it no longer gates whether the AI
	// replies at all. It used to: below confidenceThreshold skipped Gemini
	// entirely and silently handed the conversation to a human, which meant
	// a customer who just said "salom" got no reply whatsoever — indistinguishable
	// from the bot being broken. A DM automation product cannot leave DMs
	// unanswered. The actual anti-hallucination guardrail now lives entirely
	// in systemPromptTemplate's instructions (answer only from context,
	// admit when you don't know, never invent facts) — Gemini itself decides
	// per-message whether it has enough grounding to give specifics or should
	// just engage warmly and offer a human follow-up, rather than this
	// usecase deciding that up front from a single cosine-similarity number.
	confidence := topSimilarity(hits)

	productContext := uc.buildProductContext(ctx, ev.OrganizationID, ev.ConversationID)
	systemPrompt := buildSystemPrompt(hits, productContext)
	transcript := buildTranscript(history)

	start := time.Now()
	var replyText string
	var usage geminiapi.GenerateUsage
	switch {
	case isMedia:
		mediaData, mimeType, dlErr := uc.media.DownloadAttachment(ctx, *latest.AttachmentURL)
		switch {
		case dlErr == nil:
			replyText, usage, err = uc.mediaGen.GenerateWithMedia(ctx, systemPrompt, transcript, mediaData, mimeType)
		case hasText:
			// Couldn't fetch the attachment — likely the CDN URL had
			// already expired by the time this worker picked up the event
			// (see MediaFetcher's doc comment) — but there's still a
			// caption or prior conversation to answer from. Degrade to the
			// text-only path rather than failing the whole reply over a
			// media fetch.
			replyText, usage, err = uc.generator.Generate(ctx, systemPrompt, transcript)
		default:
			// No usable media, no text — nothing to reply from.
			return uc.handoff(ctx, conv)
		}
	default:
		replyText, usage, err = uc.generator.Generate(ctx, systemPrompt, transcript)
	}
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return apperror.Internal("generate ai reply", err)
	}
	replyText = stripMarkdownEmphasis(strings.TrimSpace(replyText))
	if replyText == "" {
		return uc.handoff(ctx, conv)
	}

	// Which account to send from — and how — is resolved from conv.Channel,
	// not from ev.InstagramAccountID: ev is only ever the Instagram-side
	// event shape (see InboundEvent's doc comment), so a Telegram-channel
	// conversation instead carries its own TelegramAccountID, set at
	// ingestion by telegram.WebhookUseCase.ingestMessage.
	switch conv.Channel {
	case entity.ConversationChannelTelegram:
		if err := uc.sendTelegramReply(ctx, ev.OrganizationID, conv, replyText); err != nil {
			return err
		}
	default:
		account, err := uc.accountRepo.FindByID(ctx, ev.OrganizationID, ev.InstagramAccountID)
		if err != nil {
			return err
		}
		accessToken, err := uc.encryptor.Decrypt(account.AccessTokenEncrypted)
		if err != nil {
			return apperror.Internal("decrypt instagram access token", err)
		}

		// A message that arrived as a public comment (comment-to-DM
		// automation) has no DM thread yet, so the reply has to be sent as
		// a private reply addressed to the comment — see
		// PrivateReplySender's doc comment. Every subsequent message in the
		// same conversation is an ordinary DM, which is exactly what
		// happens here: only the ingested comment carries this metadata,
		// so the next turn falls through to SendMessage below.
		if commentID := privateReplyCommentID(latest); commentID != "" && uc.privateReply != nil {
			if err := uc.privateReply.SendPrivateReply(ctx, accessToken, commentID, replyText); err != nil {
				uc.handleSendFailure(ctx, account, err)
				return apperror.Internal("send instagram private reply", err)
			}
			break
		}

		if err := uc.sender.SendMessage(ctx, accessToken, conv.CustomerIGID, replyText); err != nil {
			uc.handleSendFailure(ctx, account, err)
			return apperror.Internal("send instagram reply", err)
		}
	}

	outbound := &entity.Message{
		OrganizationID: ev.OrganizationID,
		ConversationID: conv.ID,
		Direction:      entity.MessageDirectionOutbound,
		SenderType:     entity.MessageSenderAI,
		MessageType:    entity.MessageTypeText,
		Content:        &replyText,
	}
	if err := uc.msgRepo.Create(ctx, outbound); err != nil {
		// The reply already reached the customer over Instagram — the
		// conversation record just failed to catch up. Returning the error
		// lets the consumer log/alert on it; requeueing this event would
		// re-send a duplicate reply, which the Consumer's at-least-once
		// redelivery already risks elsewhere (see its doc comment) — not
		// solved here.
		return apperror.Internal("persist outbound ai message", err)
	}

	confidenceCopy := confidence
	latencyCopy := latencyMs
	aiResp := &entity.AIResponse{
		OrganizationID:      ev.OrganizationID,
		ConversationID:      conv.ID,
		MessageID:           outbound.ID,
		MessageCreatedAt:    outbound.CreatedAt,
		ModelUsed:           geminiapi.GenerationModel,
		PromptTokens:        usage.PromptTokens,
		CompletionTokens:    usage.CompletionTokens,
		ConfidenceScore:     &confidenceCopy,
		// A reply was always sent now (see HandleInboundMessage's comment on
		// why the hard handoff-before-generating gate was removed), so this
		// no longer means "no reply was sent" — it flags "answered below the
		// grounding threshold, worth a human spot-check" for
		// DashboardAIPerformanceResponse's handoff_rate metric.
		WasHandoffTriggered: confidence < confidenceThreshold,
		LatencyMs:           &latencyCopy,
	}
	citations := citationsFromHits(hits)
	if err := uc.aiRespRepo.Create(ctx, aiResp, citations); err != nil {
		return err
	}

	// Best-effort, after the customer already has their reply — a failure
	// here must never look like a failure to respond. See
	// captureLeadIfPresent's doc comment and leadCaptureText's for why the
	// scanned text isn't always just latest.Content.
	uc.captureLeadIfPresent(ctx, conv, leadCaptureText(latest, replyText), history)

	return uc.updateConversationAfterReply(ctx, conv, replyText)
}

// handleSendFailure inspects a SendMessage error for Meta's auth-failure
// signal (Graph API error code 190) and, when found, flips the account's
// status so it stops being silently treated as healthy — the dashboard
// shows the real state, and (once cmd/token-refresh exists) an expired
// token becomes eligible for refresh instead of failing forever. Before
// this, a revoked/expired token just failed every send with Status stuck
// at "connected", with nothing anywhere surfacing that the account needs
// attention.
//
// Best-effort and deliberately swallows its own error: this runs after a
// send has already failed, so a failure to persist the status flip must
// not mask or replace the original send error, which is what the caller
// actually returns to the queue consumer.
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

// telegramAuthError is Telegram's counterpart to authError above —
// satisfied by telegramapi.APIError. A separate, smaller interface (just
// IsAuthError, no IsExpired) because bot tokens don't have Instagram's
// ~60-day-lifetime concept — there's nothing to distinguish "expired" from
// "revoked" the way Meta's subcode 463 does; every Telegram auth failure is
// the same "the token stopped working, reconnect" case.
type telegramAuthError interface {
	IsAuthError() bool
}

// sendTelegramReply is HandleInboundMessage's Telegram branch — resolves
// the sending TelegramAccount from conv.TelegramAccountID (set at
// ingestion, never nil for a Channel == ConversationChannelTelegram row —
// enforced by chk_conversations_channel_account, migration 000014),
// decrypts its bot token, and sends via the Business Bot API. Mirrors the
// Instagram branch's structure closely on purpose.
func (uc *UseCase) sendTelegramReply(ctx context.Context, orgID uuid.UUID, conv *entity.Conversation, replyText string) error {
	if conv.TelegramAccountID == nil {
		return apperror.Internal("send telegram reply", errors.New("conversation has channel=telegram but no telegram_account_id"))
	}
	account, err := uc.telegramAccounts.FindByID(ctx, orgID, *conv.TelegramAccountID)
	if err != nil {
		return err
	}
	if account.BusinessConnectionID == nil {
		// Bot token connected, but the org hasn't finished pairing it
		// inside their own Telegram app yet (see
		// entity.TelegramAccount.BusinessConnectionID's doc comment) —
		// there is genuinely nothing to send to. This shouldn't be
		// reachable in practice (no business_connection means no
		// business_message could have arrived to trigger a reply in the
		// first place), but fails loudly rather than silently no-opping if
		// it ever is.
		return apperror.Internal("send telegram reply", errors.New("telegram account has no business_connection_id"))
	}

	botToken, err := uc.encryptor.Decrypt(account.BotTokenEncrypted)
	if err != nil {
		return apperror.Internal("decrypt telegram bot token", err)
	}

	if err := uc.telegramSender.SendMessage(ctx, botToken, *account.BusinessConnectionID, conv.CustomerIGID, replyText); err != nil {
		uc.handleTelegramSendFailure(ctx, account, err)
		return apperror.Internal("send telegram reply", err)
	}
	return nil
}

// handleTelegramSendFailure mirrors handleSendFailure — see that method's
// doc comment for the full reasoning (best-effort, must not mask the
// original send error). uc.telegramAccounts is a TelegramAccountLookup
// (read-only), so flipping status requires the concrete
// repository.TelegramAccountRepository's Update — deliberately NOT added
// to TelegramAccountLookup (that interface stays read-only on purpose,
// same as every other narrow port in this file), so this method is a
// no-op if the wired implementation doesn't also satisfy an update port.
// In practice internal/di always wires the same concrete
// *postgres.TelegramAccountRepository for both TelegramAccountLookup here
// and telegram.ConnectUseCase's full repository.TelegramAccountRepository,
// so this type-asserts to reach Update rather than widening
// TelegramAccountLookup just for this one write.
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

func (uc *UseCase) handoff(ctx context.Context, conv *entity.Conversation) error {
	conv.Status = entity.ConversationStatusPendingHuman
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return apperror.Internal("mark conversation pending_human", err)
	}
	return nil
}

// leadCaptureText picks what text captureLeadIfPresent scans for a phone
// number. Normally that's just the customer's own typed text — but a phone
// number given by VOICE (or visible in a photo/video) never appears
// anywhere as Content: latest.Content stays nil for a media message (see
// entity.Message's doc comment), even though Gemini understood it fine and
// typically echoes it back for confirmation (observed live: a customer
// said their number in a voice message, the AI correctly replied quoting
// it, but no lead was ever created because the old code only ever scanned
// latest.Content, which was empty). Falling back to replyText when the
// customer's own message has no text closes that gap for voice/image/video
// leads without changing behavior for ordinary typed messages, where
// customerText is already non-empty and wins.
func leadCaptureText(latest *entity.Message, replyText string) string {
	if latest != nil {
		if text := derefOr(latest.Content, ""); text != "" {
			return text
		}
	}
	return replyText
}

// captureLeadIfPresent is fire-and-forget: if the customer's latest
// message contains what looks like a phone number, and this conversation
// doesn't already have an open (status=new) lead, record one so it shows
// up on the Leads dashboard page for a human to actually call.
//
// Deliberately does NOT touch conv.Status or stop the AI from replying on
// this thread — pending_human would do that (see HandleInboundMessage's
// AI-owns-the-conversation gate), but a lead is a parallel "someone should
// follow up by phone" signal, not a "the AI must stop talking" signal; the
// two were kept as separate concepts specifically so the AI keeps
// answering the customer's questions while a human independently works
// the lead. See entity.Lead's doc comment.
//
// Every failure here (repo error, HasOpen error, summarization error) is
// swallowed — same reasoning as buildProductContext and
// handleSendFailure: the customer's reply has already been sent by the
// time this runs, so nothing here may surface as a pipeline failure.
func (uc *UseCase) captureLeadIfPresent(ctx context.Context, conv *entity.Conversation, customerMessage string, history []*entity.Message) {
	if uc.leads == nil {
		return
	}
	phone := extractPhone(customerMessage)
	if phone == "" {
		return
	}

	hasOpen, err := uc.leads.HasOpen(ctx, conv.OrganizationID, conv.ID)
	if err != nil || hasOpen {
		return
	}

	lead := &entity.Lead{
		OrganizationID: conv.OrganizationID,
		ConversationID: conv.ID,
		Phone:          phone,
		Summary:        uc.summarizeLead(ctx, history, phone),
		Status:         entity.LeadStatusNew,
	}
	_ = uc.leads.Create(ctx, lead)
}

// leadSummaryPromptTemplate is a one-off extraction call, not a
// customer-facing reply — it deliberately does not reuse
// systemPromptTemplate's sales-persona/grounding rules, which don't apply
// here.
const leadSummaryPromptTemplate = `Summarize this Instagram DM conversation for a teammate who is about to call this customer back. In 1-2 short sentences, in the same language the customer used, say what the customer wants and any specifics they already mentioned (product, quantity, delivery address or location, timing). Do not mention or repeat the phone number itself — it's already shown separately. If nothing specific has been said yet, just say the customer should be contacted to find out what they need.

Conversation:
%s`

// leadSummarySystemPrompt is intentionally generic/short — this call has
// no persona or grounding requirements, unlike systemPromptTemplate.
const leadSummarySystemPrompt = "You write short, factual internal notes for a sales team. No greetings, no filler — just the summary requested."

func (uc *UseCase) summarizeLead(ctx context.Context, history []*entity.Message, phone string) string {
	prompt := fmt.Sprintf(leadSummaryPromptTemplate, buildTranscript(history))
	summary, _, err := uc.generator.Generate(ctx, leadSummarySystemPrompt, prompt)
	summary = strings.TrimSpace(summary)
	if err != nil || summary == "" {
		// Still a useful lead without a summary — the phone number and a
		// link to the conversation are the essential part; this is just a
		// fallback so the Leads page never shows a blank row.
		return fmt.Sprintf("Customer (%s) left their phone number — review the conversation for details.", phone)
	}
	return summary
}

// phoneRegex matches Uzbek mobile numbers in the shapes people actually
// type them in a DM: with or without a "+998"/"998" country code, with or
// without spaces/dashes/parens between groups (90 123 45 67, 90-123-45-67,
// +998901234567, etc.). It is deliberately permissive — a bare 9-digit
// group with no country code also matches, since that's how most Uzbek
// customers type their own number. The tradeoff: a random 9-digit number
// in an unrelated message could rarely false-positive into a lead; the
// cost of that is one extra row a human dismisses on the Leads page,
// which is cheaper than the alternative of a strict pattern silently
// missing real leads.
var phoneRegex = regexp.MustCompile(`(?:\+?998[\s\-]?)?\(?\d{2}\)?[\s\-]?\d{3}[\s\-]?\d{2}[\s\-]?\d{2}`)

// extractPhone returns the first phone-shaped match in text, normalized to
// +998XXXXXXXXX where the source made that unambiguous, or "" if nothing
// matched.
func extractPhone(text string) string {
	match := phoneRegex.FindString(text)
	if match == "" {
		return ""
	}

	digits := digitsOnly(match)
	switch {
	case len(digits) == 12 && strings.HasPrefix(digits, "998"):
		return "+" + digits
	case len(digits) == 10 && strings.HasPrefix(digits, "0"):
		return "+998" + digits[1:]
	case len(digits) == 9:
		return "+998" + digits
	default:
		// Matched but didn't cleanly fit a known shape (e.g. the regex
		// caught something odd) — still return it rather than silently
		// dropping a possible lead; a human reviewing the Leads page can
		// tell at a glance if it's not really a phone number.
		return digits
	}
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (uc *UseCase) updateConversationAfterReply(ctx context.Context, conv *entity.Conversation, replyText string) error {
	preview := replyText
	if len(preview) > maxReplyPreviewLen {
		preview = preview[:maxReplyPreviewLen]
	}
	now := time.Now()
	conv.LastMessageAt = &now
	conv.LastMessagePreview = &preview
	// The AI just answered on this thread — nothing new is waiting on a
	// human, so the badge clears. If the customer replies again, ingestMessage
	// increments this again on the next inbound webhook.
	conv.UnreadCount = 0
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return apperror.Internal("update conversation after ai reply", err)
	}
	return nil
}

func findMessage(history []*entity.Message, id uuid.UUID) *entity.Message {
	for _, m := range history {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func topSimilarity(hits []repository.ChunkSearchResult) float64 {
	if len(hits) == 0 {
		return 0
	}
	// Search already orders by ascending distance (descending similarity —
	// see knowledge_chunk_repository.go's Search), so hits[0] is the
	// closest match.
	return hits[0].Similarity
}

func citationsFromHits(hits []repository.ChunkSearchResult) []*entity.AIResponseCitation {
	citations := make([]*entity.AIResponseCitation, 0, len(hits))
	for _, h := range hits {
		sim := h.Similarity
		citations = append(citations, &entity.AIResponseCitation{
			KnowledgeChunkID: h.Chunk.ID,
			SimilarityScore:  &sim,
		})
	}
	return citations
}

// systemPromptTemplate keeps the persona/grounding instructions in one
// place — a senior sales-rep tone deliberately, per this product's
// positioning ("AI-powered Instagram DM Sales Agent"), not a generic
// support bot voice. It has to work with EMPTY context too (buildSystemPrompt
// passes "(no context available)" when hits is empty, or when nothing
// cleared confidenceThreshold — see the package doc comment on CONFIDENCE):
// a greeting or small talk still gets a warm, human reply and a nudge
// toward what the business sells, it just can't state specifics that
// aren't grounded. That split (always engage vs. only state grounded
// specifics) is what replaced the old hard confidence-gate handoff.
const systemPromptTemplate = `You are the Instagram DM sales rep for this business — think of the tone of a sharp, senior salesperson/community manager who's great at DMs, not a generic support bot and not a robotic FAQ machine.

Rules:
- Every message gets a real reply — never leave the customer hanging, even a bare "salom" or "hi" deserves a warm, genuine response.
- State specific facts (prices, hours, policies, stock, delivery times, etc.) ONLY from the context below. Never invent, estimate, or guess a specific fact that isn't in it.
- If the customer asks something specific the context doesn't cover, don't just say "I don't know" — acknowledge what they asked, say a team member will follow up with the exact details, and keep the conversation warm and moving (e.g. ask a clarifying question, or highlight something you DO know that's relevant).
- If there's little or no relevant context (a greeting, small talk, or an off-topic message), don't wait for a "real question" — greet them back like a person would, show genuine interest, and naturally invite them to share what they're looking for. This is where a good sales rep builds rapport, not where the bot goes silent.
- Keep replies short and conversational, like a real Instagram DM — not an email. A sentence or two, occasionally three.
- Always look for a natural, non-pushy opening to move the conversation toward a sale — a next step, a question that surfaces their need, or a relevant detail that creates interest.
- Match the customer's language and tone (e.g. reply in Uzbek if they wrote in Uzbek).
- Never use markdown formatting — no **bold**, no *italics*, no bullet points, no headers. Instagram and Telegram DMs render plain text only, so any asterisks or underscores you write for emphasis show up literally to the customer instead of being rendered — write plain sentences instead.
- Do not mention that you are an AI, a language model, or that you're using "context" or "documents" — just answer naturally, like a person on the team would.
- Payment links are dangerous to get wrong: only ever send a URL that appears character-for-character in the "Products" section below. NEVER type out, construct, guess, modify, or shorten a payment link yourself, even partially — copy it exactly or don't send one. If a customer wants to pay for something that either isn't in the Products list, or is listed there WITHOUT a payment link, do not invent one — tell them warmly that a team member will send payment details shortly.
- When the customer's latest message includes a photo, actually look at it and respond to what's in it as a natural part of the conversation — it might be a product they're asking about, a screenshot of a question, a payment receipt, a size/color they're showing you, etc. Don't ignore the image and only answer any accompanying text.
- When the customer's latest message is a voice message, actually listen to it and respond to what they said, the same as if they'd typed it — including matching whatever language they spoke in, not just the language of earlier text in this conversation.
- When the customer's latest message is a video, actually watch it and respond to what's shown/said in it, the same as you would a photo or voice message — it might be a product demo, an unboxing question, a problem they're showing you, etc.

Products:
%s

Context:
%s`

func buildSystemPrompt(hits []repository.ChunkSearchResult, productContext string) string {
	context := "(no context available)"
	if len(hits) > 0 {
		var b strings.Builder
		for i, h := range hits {
			if i > 0 {
				b.WriteString("\n---\n")
			}
			b.WriteString(h.Chunk.Content)
		}
		context = b.String()
	}
	if productContext == "" {
		productContext = "(this business has no products listed yet)"
	}
	return fmt.Sprintf(systemPromptTemplate, productContext, context)
}

// buildProductContext is the structured (non-RAG) counterpart to the
// embedding-retrieved hits above — see entity.Product's doc comment on why
// a compact price catalog is enumerated directly rather than hoping the
// right chunk gets retrieved. Best-effort like the username-resolve block
// in instagram.WebhookUseCase: a lookup failure here must not block the
// reply, so both repo calls swallow their own errors (returning "" falls
// through to buildSystemPrompt's "(this business has no products listed
// yet)" placeholder — the safe, conservative default that also happens to
// satisfy "don't offer a payment link" when something's actually wrong).
//
// Caps at maxProductsInPrompt so a large catalog can't blow up prompt size
// or cost — real pagination/relevance-ranking for bigger catalogs is a
// follow-up, not needed for an MVP-sized shop.
func (uc *UseCase) buildProductContext(ctx context.Context, orgID, conversationID uuid.UUID) string {
	if uc.products == nil {
		return ""
	}
	products, err := uc.products.ListActiveByOrganization(ctx, orgID)
	if err != nil || len(products) == 0 {
		return ""
	}
	if len(products) > maxProductsInPrompt {
		products = products[:maxProductsInPrompt]
	}

	var clickIntegration *entity.ClickIntegration
	if uc.click != nil {
		if ci, err := uc.click.FindByOrganization(ctx, orgID); err == nil {
			clickIntegration = ci
		}
	}

	var b strings.Builder
	for i, p := range products {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- %s — %s so'm", p.Name, formatSom(p.PriceCents))
		if p.Description != nil && strings.TrimSpace(*p.Description) != "" {
			fmt.Fprintf(&b, " (%s)", strings.TrimSpace(*p.Description))
		}
		if clickIntegration != nil {
			link := clickapi.BuildPaymentLink(clickapi.PaymentLinkInput{
				MerchantID:       clickIntegration.MerchantID,
				ServiceID:        clickIntegration.ServiceID,
				MerchantUserID:   derefOr(clickIntegration.MerchantUserID, ""),
				Amount:           clickapi.FormatAmount(p.PriceCents),
				// See clickapi.BuildTransactionParam's doc comment — kept as
				// the single source of truth for this format so
				// payment.WebhookUseCase's ParseTransactionParam can never
				// drift out of sync with how this string is built.
				TransactionParam: clickapi.BuildTransactionParam(conversationID, p.ID),
			})
			fmt.Fprintf(&b, "\n  Payment link (send this EXACT text if they want to buy this): %s", link)
		}
	}
	return b.String()
}

// maxProductsInPrompt bounds buildProductContext — see its doc comment.
const maxProductsInPrompt = 50

// formatSom renders a price_cents integer (tiyin) as a plain so'm string
// with no decimals shown when the tiyin part is zero — "150000" not
// "150000.00" — since UZS retail prices are essentially never quoted with
// sub-so'm precision in a DM. Falls back to showing the tiyin remainder
// when it's non-zero rather than silently truncating it.
func formatSom(priceCents int64) string {
	som := priceCents / 100
	remainder := priceCents % 100
	if remainder == 0 {
		return fmt.Sprintf("%d", som)
	}
	return fmt.Sprintf("%d.%02d", som, remainder)
}

// markdownEmphasisRegex matches **bold**, __bold__, *italic*, or _italic_
// spans and captures just the inner text. Instagram and Telegram DMs both
// render as plain text — the customer sees the literal asterisks/
// underscores, not bold text — and systemPromptTemplate now explicitly
// tells Gemini not to use markdown, but this is a backstop for whenever it
// does anyway (observed live: a reply confirming a phone number came back
// as "**+998 93 444 56 64**", which read to the customer, and in the
// dashboard, as the number wrapped in literal asterisks — easy to misread
// as the digits being partially masked/hidden). Deliberately narrow (just
// emphasis markers, not a full markdown-to-plaintext pass) — headers,
// lists, links etc. are not things this product's prompt asks Gemini to
// produce in the first place.
var markdownEmphasisRegex = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__|\*(.+?)\*|_(.+?)_`)

func stripMarkdownEmphasis(text string) string {
	return markdownEmphasisRegex.ReplaceAllStringFunc(text, func(match string) string {
		groups := markdownEmphasisRegex.FindStringSubmatch(match)
		for _, g := range groups[1:] {
			if g != "" {
				return g
			}
		}
		return match
	})
}

// privateReplyCommentID reads the comment id an ingested Instagram comment
// carries in its metadata (written by instagram.WebhookUseCase.handleComment
// — see metadataKeyPrivateReplyCommentID). Returns "" for an ordinary DM,
// which is every message except the one that started a comment-to-DM
// conversation.
func privateReplyCommentID(latest *entity.Message) string {
	if latest == nil || latest.Metadata == nil {
		return ""
	}
	id, ok := latest.Metadata[metadataKeyPrivateReplyCommentID].(string)
	if !ok {
		return ""
	}
	return id
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// buildTranscript turns the last few turns (newest-first, per
// MessageRepository.List) into a plain-text, oldest-first transcript —
// Gemini's generateContent is called with this as the single "user" turn
// rather than one Content per historical message, since only the system
// instruction/context needs structuring for this usecase's purposes; a
// full multi-turn Contents array is a reasonable future improvement, not
// needed for a first working version.
func buildTranscript(history []*entity.Message) string {
	var b strings.Builder
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		speaker := "Customer"
		if m.Direction == entity.MessageDirectionOutbound {
			speaker = "You"
		}
		switch {
		case m.Content != nil && strings.TrimSpace(*m.Content) != "":
			fmt.Fprintf(&b, "%s: %s\n", speaker, *m.Content)
		case m.MessageType == entity.MessageTypeImage:
			// A captionless image/voice message previously vanished from
			// the transcript entirely (this branch used to just `continue`
			// on empty Content) — leaving a marker means a later text-only
			// reply in the same conversation still has a record that
			// something was sent, even though this function only carries
			// plain text and can't re-embed the media itself here (the
			// media bytes are sent to Gemini separately — see
			// HandleInboundMessage's isMedia branch — only for the turn
			// currently being answered).
			fmt.Fprintf(&b, "%s: [sent a photo]\n", speaker)
		case m.MessageType == entity.MessageTypeAudio:
			fmt.Fprintf(&b, "%s: [sent a voice message]\n", speaker)
		case m.MessageType == entity.MessageTypeVideo:
			fmt.Fprintf(&b, "%s: [sent a video]\n", speaker)
		default:
			// A file attachment with no caption, or any other empty-content
			// message — nothing textual to contribute yet.
		}
	}
	return strings.TrimSpace(b.String())
}
