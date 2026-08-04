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
	convRepo    repository.ConversationRepository
	msgRepo     repository.MessageRepository
	accountRepo repository.InstagramAccountRepository
	aiRespRepo  repository.AIResponseRepository
	retriever   Retriever
	generator   Generator
	sender      Sender
	encryptor   *crypto.AESGCMEncryptor
	products    ProductLister
	click       ClickIntegrationLookup
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
) *UseCase {
	return &UseCase{
		convRepo:    convRepo,
		msgRepo:     msgRepo,
		accountRepo: accountRepo,
		aiRespRepo:  aiRespRepo,
		retriever:   retriever,
		generator:   generator,
		sender:      sender,
		encryptor:   encryptor,
		products:    products,
		click:       click,
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
	if latest == nil || latest.Content == nil || strings.TrimSpace(*latest.Content) == "" {
		// Nothing to respond to — e.g. an image/story-reply with no text
		// content, or (shouldn't happen, but fail safe) the event's message
		// isn't in the fetched history window.
		return nil
	}

	hits, err := uc.retriever.Search(ctx, ev.OrganizationID, *latest.Content, retrievalLimit)
	if err != nil {
		return err
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
	replyText, usage, err := uc.generator.Generate(ctx, systemPrompt, transcript)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		return apperror.Internal("generate ai reply", err)
	}
	replyText = strings.TrimSpace(replyText)
	if replyText == "" {
		return uc.handoff(ctx, conv)
	}

	account, err := uc.accountRepo.FindByID(ctx, ev.OrganizationID, ev.InstagramAccountID)
	if err != nil {
		return err
	}
	accessToken, err := uc.encryptor.Decrypt(account.AccessTokenEncrypted)
	if err != nil {
		return apperror.Internal("decrypt instagram access token", err)
	}

	if err := uc.sender.SendMessage(ctx, accessToken, conv.CustomerIGID, replyText); err != nil {
		uc.handleSendFailure(ctx, account, err)
		return apperror.Internal("send instagram reply", err)
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

func (uc *UseCase) handoff(ctx context.Context, conv *entity.Conversation) error {
	conv.Status = entity.ConversationStatusPendingHuman
	if err := uc.convRepo.Update(ctx, conv); err != nil {
		return apperror.Internal("mark conversation pending_human", err)
	}
	return nil
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
- Do not mention that you are an AI, a language model, or that you're using "context" or "documents" — just answer naturally, like a person on the team would.
- Payment links are dangerous to get wrong: only ever send a URL that appears character-for-character in the "Products" section below. NEVER type out, construct, guess, modify, or shorten a payment link yourself, even partially — copy it exactly or don't send one. If a customer wants to pay for something that either isn't in the Products list, or is listed there WITHOUT a payment link, do not invent one — tell them warmly that a team member will send payment details shortly.

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
				TransactionParam: conversationID.String() + "-" + p.ID.String(),
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
		if m.Content == nil || strings.TrimSpace(*m.Content) == "" {
			continue
		}
		speaker := "Customer"
		if m.Direction == entity.MessageDirectionOutbound {
			speaker = "You"
		}
		fmt.Fprintf(&b, "%s: %s\n", speaker, *m.Content)
	}
	return strings.TrimSpace(b.String())
}
