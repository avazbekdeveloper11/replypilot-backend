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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	// GenerationModel was "gemini-2.0-flash" until Google shut it down
	// June 1, 2026 (confirmed live: a real request against it now 404s with
	// "This model models/gemini-2.0-flash is no longer available"; caught
	// in production when a customer's message got no reply — see the AI
	// pipeline's error logs). gemini-2.5-flash is the current replacement.
	// IMPORTANT: gemini-2.5-flash is itself scheduled for shutdown October
	// 16, 2026, with gemini-3.5-flash as its replacement — this will need
	// updating again before then. gemini-3.5-flash was available at time of
	// writing but reported ~15x pricier per token than 2.0-flash was, so
	// migrating early isn't free — worth checking actual gemini-3.5-flash
	// pricing against usage volume before jumping straight to it.
	GenerationModel = "gemini-2.5-flash"
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
	Contents         []generateContent `json:"contents"`
	SystemInstruction *generateContent  `json:"systemInstruction,omitempty"`
}

type generateContent struct {
	Role  string             `json:"role,omitempty"`
	Parts []generateTextPart `json:"parts"`
}

type generateTextPart struct {
	Text string `json:"text"`
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

// Generate calls gemini-2.0-flash with a system prompt (the knowledge-base
// context + instructions) and a single user turn (the customer's
// message), returning the model's plain-text reply plus token usage. No
// multi-turn history or function calling here — internal/usecase/ai.UseCase
// is what decides what goes into systemPrompt (retrieved chunks, tone
// instructions, etc.) and userMessage (the conversation transcript), this
// client is deliberately just the transport.
func (c *Client) Generate(ctx context.Context, systemPrompt, userMessage string) (string, GenerateUsage, error) {
	reqBody, err := json.Marshal(generateRequest{
		SystemInstruction: &generateContent{Parts: []generateTextPart{{Text: systemPrompt}}},
		Contents: []generateContent{
			{Role: "user", Parts: []generateTextPart{{Text: userMessage}}},
		},
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
