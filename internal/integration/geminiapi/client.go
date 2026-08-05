// Package geminiapi is the concrete adapter for Google's Gemini API —
// text embeddings (used by the knowledge base ingestion pipeline,
// internal/usecase/knowledgebase) and reply generation (used by the AI
// reply pipeline, internal/usecase/ai). Plain net/http, no Google SDK
// dependency — same reasoning as internal/integration/metaapi: this
// codebase has no Go toolchain available to verify a new dependency
// resolves and compiles, so every external API integration here is a
// small hand-written REST client instead.
package geminiapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// EmbeddingDimensions must match migration 000005's
// knowledge_base_chunks.embedding column (vector(768)). gemini-embedding-001
// defaults to 3072 dimensions — Embed explicitly requests 768 via
// embedContentConfig.outputDimensionality (Matryoshka truncation, per
// Google's docs) to keep that column's shape unchanged rather than
// requiring a migration. If this ever changes, migration 000005 (and its
// HNSW index) has to change with it; see that migration's doc comment.
const EmbeddingDimensions = 768

const (
	// embeddingModel was "text-embedding-004" until Google retired it for
	// the Gemini API (confirmed live: a real request against it now 404s
	// with "is not found for API version v1beta, or is not supported for
	// embedContent" — this codebase hit that in production, not a
	// hypothetical). gemini-embedding-001 is the current replacement per
	// ai.google.dev/api/embeddings.
	embeddingModel = "gemini-embedding-001"
	// GenerationModel went through two dead ends before landing here, both
	// confirmed live against this project's own API key, not assumed from
	// docs:
	//   "gemini-2.0-flash" -> 404 "no longer available" (Google shut it
	//   down June 1, 2026).
	//   "gemini-2.5-flash" -> 404 "no longer available to new users" (this
	//   project's key was provisioned after Google cut off new access to
	//   it, even though existing callers may still be grandfathered in —
	//   don't be surprised if an older project's key behaves differently).
	// "gemini-3.5-flash" is the current GA model and what's live-verified
	// working as of this writing. It reportedly prices meaningfully higher
	// per token than 2.0-flash did — worth checking actual usage cost after
	// a few weeks live, but there was no cheaper GA alternative available
	// to this key at fix time.
	GenerationModel = "gemini-3.5-flash"
	baseURL         = "https://generativelanguage.googleapis.com/v1beta"
)

// Client's apiKey is mutex-protected, not a plain string field, because it
// can change after construction: internal/platform/geminikey polls
// internal/usecase/platformsettings for an admin-configured key and calls
// SetAPIKey when it changes, so a key rotated from the admin panel takes
// effect for both cmd/api and cmd/worker-ai without a restart. Embed/
// Generate read the key fresh on every call via currentAPIKey — there is
// no caching beyond what the poller itself does.
type Client struct {
	httpClient *http.Client

	mu     sync.RWMutex
	apiKey string
}

func NewClient(apiKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiKey:     apiKey,
	}
}

// SetAPIKey replaces the key used by every subsequent call. Safe to call
// from a different goroutine than the one making requests — see
// internal/platform/geminikey.
func (c *Client) SetAPIKey(apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.apiKey = apiKey
}

func (c *Client) currentAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiKey
}

type embedRequest struct {
	Model              string             `json:"model"`
	Content            embedContent       `json:"content"`
	EmbedContentConfig embedContentConfig `json:"embedContentConfig"`
}

type embedContent struct {
	Parts []embedPart `json:"parts"`
}

type embedPart struct {
	Text string `json:"text"`
}

// embedContentConfig is where output-shaping options live per the current
// API (the old top-level outputDimensionality request field is deprecated
// in favor of this nested object — see ai.google.dev/api/embeddings).
type embedContentConfig struct {
	OutputDimensionality int `json:"outputDimensionality"`
}

type embedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}

// Embed returns a 768-dimensional vector for text via Gemini's
// gemini-embedding-001 model, truncated down from its native 3072
// dimensions via outputDimensionality (see EmbeddingDimensions' doc
// comment). Used both to embed knowledge-base chunks at ingestion time and
// to embed a customer's message at query time (RAG retrieval needs the
// same embedding space for both sides of the similarity search).
//
// Truncated Matryoshka embeddings are, per Google's docs, not
// re-normalized after truncation — that only matters for magnitude-
// sensitive comparisons (raw dot product, L2 distance). This codebase's
// only consumer, KnowledgeChunkRepository.Search, orders by pgvector's
// cosine distance (`<=>`), which divides out vector magnitude as part of
// its own computation — so an un-renormalized truncated vector compares
// correctly here without an extra normalization step.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	reqBody, err := json.Marshal(embedRequest{
		Model:              "models/" + embeddingModel,
		Content:            embedContent{Parts: []embedPart{{Text: text}}},
		EmbedContentConfig: embedContentConfig{OutputDimensionality: EmbeddingDimensions},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:embedContent?key=%s", baseURL, embeddingModel, c.currentAPIKey())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var result embedResponse
	if err := c.doJSON(req, &result); err != nil {
		return nil, fmt.Errorf("gemini embed: %w", err)
	}

	// The embedContentConfig.outputDimensionality request field (set
	// above) is documented to truncate server-side, but this was observed
	// live NOT taking effect — Gemini returned its native 3072-dimensional
	// vector regardless. Rather than depend on the server honoring that
	// field, truncate here ourselves: gemini-embedding-001 is a Matryoshka
	// embedding, meaning its dimensions are ordered by importance and a
	// prefix truncation is a valid, well-defined embedding in its own
	// right (this is exactly what the server-side option is documented to
	// do — "excessive values ... truncated from the end" — we're just not
	// trusting the request field to trigger it). See Embed's doc comment
	// on why no re-normalization step is needed for this codebase's
	// cosine-distance-based search.
	switch {
	case len(result.Embedding.Values) < EmbeddingDimensions:
		return nil, fmt.Errorf("gemini embed: expected at least %d dimensions, got %d", EmbeddingDimensions, len(result.Embedding.Values))
	case len(result.Embedding.Values) > EmbeddingDimensions:
		return result.Embedding.Values[:EmbeddingDimensions], nil
	default:
		return result.Embedding.Values, nil
	}
}

type generateRequest struct {
	Contents          []generateContent `json:"contents"`
	SystemInstruction *generateContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

// generationConfig currently exists for exactly one field: thinkingConfig.
// gemini-3.5-flash "thinks" by default even for a two-word DM reply — a
// live test against this project's own key ("salom" in, a 15-token reply
// out) reported usageMetadata.thoughtsTokenCount: 347, i.e. the hidden
// reasoning cost was >20x the visible reply. That's pure latency and
// spend with no benefit for a short, conversational DM reply (this isn't
// a math/coding task), so thinkingBudget is forced to 0 in Generate.
type generationConfig struct {
	ThinkingConfig *thinkingConfig `json:"thinkingConfig,omitempty"`
}

// ThinkingBudget: 0 disables thinking outright, per
// ai.google.dev/gemini-api/docs/thinking. If a future prompt genuinely
// needs multi-step reasoning (not the case for this pipeline today), raise
// this rather than removing it — don't just delete generationConfig.
type thinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type generateContent struct {
	Role  string         `json:"role,omitempty"`
	Parts []generatePart `json:"parts"`
}

// generatePart is a union of Gemini's two part shapes this client sends:
// plain text, or an inline (base64) media blob for multimodal input (see
// GenerateWithMedia). Exactly one of Text/InlineData should be set per
// part — omitempty keeps the unused one out of the JSON entirely, since
// Gemini's API rejects a part object with both empty and populated
// mutually-exclusive fields present.
type generatePart struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

// inlineData is Gemini's format for embedding raw media bytes directly in
// the request (as opposed to the separate Files API, which uploads once
// and references by URI — not used here since a DM attachment is fetched
// once and used once, not worth a two-step upload-then-reference for
// this codebase's ~one-shot use). MimeType must be one Gemini recognizes
// for the model in use; Data is standard base64, no data-URL prefix.
type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type generateResponse struct {
	Candidates []struct {
		Content generateContent `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

// GenerateUsage carries Gemini's own reported token counts for one
// Generate call — real numbers from the API response, not an estimate
// (contrast internal/usecase/knowledgebase's chunkText, which DOES
// estimate token count since embedContent's response has no usage field).
type GenerateUsage struct {
	PromptTokens     int
	CompletionTokens int
}

// Generate calls Gemini with a system prompt (the knowledge-base context +
// instructions) and a single user turn (the customer's message), returning
// the model's plain-text reply plus token usage. No multi-turn history or
// function calling here — internal/usecase/ai.UseCase is what decides what
// goes into systemPrompt (retrieved chunks, tone instructions, etc.) and
// userMessage (the conversation transcript), this client is deliberately
// just the transport.
func (c *Client) Generate(ctx context.Context, systemPrompt, userMessage string) (string, GenerateUsage, error) {
	return c.generate(ctx, systemPrompt, []generatePart{{Text: userMessage}})
}

// GenerateWithMedia is Generate's multimodal sibling: the user turn carries
// one inline media blob alongside (optionally empty) text, so Gemini
// reasons about both together in one call — used when internal/usecase/ai
// determines the customer's latest message is an image or a voice message
// (see ai.UseCase.HandleInboundMessage). Originally named GenerateWithImage
// and image-only; generalized once voice support needed the exact same
// request shape — Gemini's inlineData mechanism doesn't care whether the
// bytes are a photo or an audio clip, only mediaMimeType changes.
//
// userMessage may be "" (media with no caption); mediaData is the raw
// downloaded bytes (not base64 yet — that happens here) and mediaMimeType
// is whatever the source served (e.g. "image/jpeg", "audio/mp4"), passed
// straight through to Gemini rather than re-detected, since re-sniffing
// content-type from bytes is one more way to get it wrong for no benefit.
//
// Audio caveat: Gemini's documented natively-supported audio MIME types
// are wav/mp3/aiff/aac/ogg/flac (ai.google.dev/gemini-api/docs/audio).
// Instagram voice-message attachments have not been confirmed live against
// this project's key as of this writing — if Meta's CDN serves a
// Content-Type Gemini rejects, this call returns an error, which
// HandleInboundMessage's isMedia branch already degrades out of safely
// (falls back to a text-only reply if there's a caption/history, or hands
// off to a human otherwise) rather than crashing the pipeline. Worth
// checking real logs after this ships, same spirit as GenerationModel's
// doc comment on why "confirmed live" matters more than what docs claim.
func (c *Client) GenerateWithMedia(ctx context.Context, systemPrompt, userMessage string, mediaData []byte, mediaMimeType string) (string, GenerateUsage, error) {
	parts := make([]generatePart, 0, 2)
	if strings.TrimSpace(userMessage) != "" {
		parts = append(parts, generatePart{Text: userMessage})
	}
	parts = append(parts, generatePart{InlineData: &inlineData{
		MimeType: mediaMimeType,
		Data:     base64.StdEncoding.EncodeToString(mediaData),
	}})
	return c.generate(ctx, systemPrompt, parts)
}

func (c *Client) generate(ctx context.Context, systemPrompt string, parts []generatePart) (string, GenerateUsage, error) {
	reqBody, err := json.Marshal(generateRequest{
		SystemInstruction: &generateContent{Parts: []generatePart{{Text: systemPrompt}}},
		Contents: []generateContent{
			{Role: "user", Parts: parts},
		},
		// See generationConfig's doc comment: thinking is pure overhead for
		// a short conversational DM reply.
		GenerationConfig: &generationConfig{ThinkingConfig: &thinkingConfig{ThinkingBudget: 0}},
	})
	if err != nil {
		return "", GenerateUsage{}, fmt.Errorf("marshal generate request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", baseURL, GenerationModel, c.currentAPIKey())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", GenerateUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var result generateResponse
	if err := c.doJSON(req, &result); err != nil {
		return "", GenerateUsage{}, fmt.Errorf("gemini generate: %w", err)
	}
	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", GenerateUsage{}, fmt.Errorf("gemini generate: no candidates returned")
	}
	usage := GenerateUsage{
		PromptTokens:     result.UsageMetadata.PromptTokenCount,
		CompletionTokens: result.UsageMetadata.CandidatesTokenCount,
	}
	return result.Candidates[0].Content.Parts[0].Text, usage, nil
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
		return fmt.Errorf("gemini api returned %d: %s", resp.StatusCode, string(body))
	}

	return json.Unmarshal(body, out)
}
