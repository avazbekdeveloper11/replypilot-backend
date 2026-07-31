// Package ai is the AI reply pipeline's core logic: given an inbound
// customer message, retrieve grounding context from the org's knowledge
// base (RAG), decide whether the model has enough grounding to answer
// confidently, generate a reply with Gemini if so, send it back to the
// customer via Instagram, and record what happened.
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
//	message. Below confidenceThreshold (or zero chunks retrieved — e.g. an
//	empty knowledge base), the usecase hands the conversation off to a
//	human WITHOUT calling Gemini at all, rather than generating a possibly
//	ungrounded answer. This also means a genuinely empty knowledge base
//	fails safe: every conversation hands off immediately instead of the AI
//	guessing.
//
// HANDOFF AND ai_responses — READ BEFORE ASSUMING EVERY HANDOFF IS LOGGED
//
//	ai_responses.message_id is a NOT NULL composite FK into the partitioned
//	messages table (see migrations/000001's trade-off note) — a row here
//	can only exist for an actual sent message. A low-confidence handoff
//	produces no reply and therefore no message, so no ai_responses row is
//	created for it either; only conversations.status flips to
//	pending_human. Consequently DashboardAIPerformanceResponse's
//	handoff_rate (computed from ai_responses.was_handoff_triggered)
//	undercounts these "never even attempted" handoffs — it only reflects a
//	handoff decided AFTER a reply was generated, which this pipeline
//	currently never does (it decides handoff BEFORE generating). If that
//	distinction matters for reporting later, add a dedicated handoff-events
//	table rather than stretching ai_responses to cover a case its schema
//	doesn't fit.
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

	confidence := topSimilarity(hits)
	if confidence < confidenceThreshold {
		return uc.handoff(ctx, conv)
	}

	systemPrompt := buildSystemPrompt(hits)
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
		WasHandoffTriggered: false,
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
// place — a sales-agent tone deliberately, per this product's positioning
// ("AI-powered Instagram DM Sales Agent"), not a generic support bot voice.
const systemPromptTemplate = `You are ReplyPilot, an AI sales assistant replying to Instagram DMs on behalf of a business.

Rules:
- Answer ONLY using the context below. If the context doesn't contain the answer, say you'll have a team member follow up — never invent facts, prices, or policies.
- Keep replies short and conversational, like a real Instagram DM — not an email. A sentence or two, occasionally three.
- Be warm and helpful, and look for a natural opening to move the conversation toward a sale (e.g. suggesting a next step), but never be pushy.
- Do not mention that you are an AI, a language model, or that you're using "context" or "documents" — just answer naturally.

Context:
%s`

func buildSystemPrompt(hits []repository.ChunkSearchResult) string {
	if len(hits) == 0 {
		return fmt.Sprintf(systemPromptTemplate, "(no context available)")
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(h.Chunk.Content)
	}
	return fmt.Sprintf(systemPromptTemplate, b.String())
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
