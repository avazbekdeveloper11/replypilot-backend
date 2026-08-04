// Package knowledgebase ingests text into searchable, embedded chunks and
// serves the CRUD behind the Knowledge Base page. Only KBSourceTypeManualText
// (pasted text) and KBSourceTypeFile (a plain .txt/.md upload, already
// read into a UTF-8 string by the handler) are actually implemented —
// KBSourceTypeURL (scrape a page) and KBSourceTypeFAQ (structured Q&A
// entries) exist in the schema/entity enum but have no ingestion code
// here yet. See docs/KNOWLEDGE_BASE_MILESTONE.md.
package knowledgebase

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

// chunkSize/chunkOverlap are in RUNES, not tokens — this is a simple
// fixed-size sliding-window chunker, not a sentence/paragraph-aware one.
// Good enough for an MVP corpus; a smarter chunker (respecting sentence
// boundaries, headings, etc.) is a real follow-up, not implemented here.
const (
	chunkSize    = 1500
	chunkOverlap = 200
)

// Embedder is the one capability this usecase needs from Gemini — kept as
// a small interface (not a direct dependency on geminiapi.Client) so the
// usecase layer stays framework/vendor-agnostic, same convention as every
// other usecase in this codebase depending on repository interfaces, not
// concrete postgres/redis types.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type UseCase struct {
	docRepo   repository.KnowledgeDocumentRepository
	chunkRepo repository.KnowledgeChunkRepository
	embedder  Embedder
}

func New(docRepo repository.KnowledgeDocumentRepository, chunkRepo repository.KnowledgeChunkRepository, embedder Embedder) *UseCase {
	return &UseCase{docRepo: docRepo, chunkRepo: chunkRepo, embedder: embedder}
}

type UploadInput struct {
	OrganizationID uuid.UUID
	UploadedBy     uuid.UUID
	Title          string
	Content        string // already-extracted plain text (pasted, or a .txt/.md file's raw bytes as UTF-8)
	SourceType     entity.KBSourceType
}

// Upload ingests synchronously — chunk, embed every chunk (one Gemini call
// each), write. This codebase has a RabbitMQ publisher for other flows but
// no background worker for knowledge-base ingestion, so a large document
// makes this call slow rather than returning immediately with a
// "processing" status the UI polls. Honest tradeoff for an MVP-sized
// corpus; move this behind a queue if documents get big enough that
// upload requests start timing out.
func (uc *UseCase) Upload(ctx context.Context, in UploadInput) (*entity.KnowledgeDocument, error) {
	uploadedBy := in.UploadedBy
	content := in.Content
	doc := &entity.KnowledgeDocument{
		OrganizationID: in.OrganizationID,
		Title:          in.Title,
		SourceType:     in.SourceType,
		// Stored up front, before ingestion even starts, so the raw text
		// survives even if chunking/embedding fails partway through — see
		// entity.KnowledgeDocument.Content's doc comment.
		Content:    &content,
		Status:     entity.KBDocumentStatusPending,
		UploadedBy: &uploadedBy,
	}
	if err := uc.docRepo.Create(ctx, doc); err != nil {
		return nil, err
	}

	if err := uc.ingest(ctx, doc, in.Content); err != nil {
		doc.Status = entity.KBDocumentStatusFailed
		msg := err.Error()
		doc.ErrorMessage = &msg
		_ = uc.docRepo.Update(ctx, doc) // best-effort; the caller sees the original ingest error below
		return nil, apperror.Internal("ingest knowledge document", err)
	}

	return doc, nil
}

func (uc *UseCase) ingest(ctx context.Context, doc *entity.KnowledgeDocument, content string) error {
	doc.Status = entity.KBDocumentStatusProcessing
	if err := uc.docRepo.Update(ctx, doc); err != nil {
		return err
	}

	pieces := chunkText(content)
	if len(pieces) == 0 {
		return fmt.Errorf("document has no extractable text content")
	}

	chunks := make([]*entity.KnowledgeChunk, 0, len(pieces))
	embeddings := make([][]float32, 0, len(pieces))
	for i, piece := range pieces {
		vec, err := uc.embedder.Embed(ctx, piece)
		if err != nil {
			return fmt.Errorf("embed chunk %d: %w", i, err)
		}
		// len(piece)/4 is a rough characters-per-token estimate, not a
		// real tokenizer (tiktoken et al.) — this codebase has no
		// tokenizer dependency. Good enough for the UI to show an
		// approximate size; not used for anything billing-accurate.
		tokenCount := len(piece) / 4
		chunks = append(chunks, &entity.KnowledgeChunk{
			OrganizationID: doc.OrganizationID,
			DocumentID:     doc.ID,
			ChunkIndex:     i,
			Content:        piece,
			TokenCount:     &tokenCount,
		})
		embeddings = append(embeddings, vec)
	}

	if err := uc.chunkRepo.CreateBatch(ctx, chunks, embeddings); err != nil {
		return err
	}

	doc.Status = entity.KBDocumentStatusReady
	doc.ErrorMessage = nil
	return uc.docRepo.Update(ctx, doc)
}

type UpdateInput struct {
	Title string
	// Content == nil means "leave the text/chunks alone, only the title
	// changed" — no re-ingestion, just a metadata update. Content !=
	// nil (including a pointer to "") triggers a full re-chunk +
	// re-embed, same pipeline Upload uses, because the old chunks no
	// longer match the new text and there is no cheap way to diff and
	// patch embeddings in place.
	Content *string
}

// Update edits an existing document's title and, optionally, its text.
// Editing text discards and rebuilds every chunk/embedding for this
// document — same cost as a fresh Upload — so this can take a moment for
// a large document, same caveat as Upload's doc comment.
func (uc *UseCase) Update(ctx context.Context, orgID, id uuid.UUID, in UpdateInput) (*entity.KnowledgeDocument, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, apperror.InvalidInput("title is required", nil)
	}

	doc, err := uc.docRepo.FindByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	doc.Title = title

	if in.Content == nil {
		if err := uc.docRepo.Update(ctx, doc); err != nil {
			return nil, err
		}
		return doc, nil
	}

	content := strings.TrimSpace(*in.Content)
	if content == "" {
		return nil, apperror.InvalidInput("content cannot be empty", nil)
	}

	// The existing chunks were embedded from the old text — once it
	// changes they're not "stale versions", they're wrong, so they're
	// deleted outright rather than left around until ingest succeeds.
	// Matches Delete's same ordering (chunks first, so a document is
	// never left referencing embeddings from different text than what
	// its own content column says).
	if err := uc.chunkRepo.DeleteByDocument(ctx, orgID, id); err != nil {
		return nil, err
	}
	doc.Content = &content

	if err := uc.ingest(ctx, doc, content); err != nil {
		doc.Status = entity.KBDocumentStatusFailed
		msg := err.Error()
		doc.ErrorMessage = &msg
		_ = uc.docRepo.Update(ctx, doc) // best-effort; caller sees the ingest error below
		return nil, apperror.Internal("re-ingest knowledge document", err)
	}

	return doc, nil
}

func (uc *UseCase) List(ctx context.Context, orgID uuid.UUID) ([]*entity.KnowledgeDocument, error) {
	return uc.docRepo.ListByOrganization(ctx, orgID)
}

func (uc *UseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*entity.KnowledgeDocument, error) {
	return uc.docRepo.FindByID(ctx, orgID, id)
}

func (uc *UseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	if _, err := uc.docRepo.FindByID(ctx, orgID, id); err != nil {
		return err
	}
	if err := uc.chunkRepo.DeleteByDocument(ctx, orgID, id); err != nil {
		return err
	}
	return uc.docRepo.Delete(ctx, orgID, id)
}

// Search is the RAG retrieval entry point — embeds the query, finds the
// closest chunks. Not called by anything yet in this milestone; the AI
// reply pipeline (internal/usecase/ai) is what calls it.
func (uc *UseCase) Search(ctx context.Context, orgID uuid.UUID, query string, limit int) ([]repository.ChunkSearchResult, error) {
	vec, err := uc.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	return uc.chunkRepo.Search(ctx, orgID, vec, limit)
}

func chunkText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	runes := []rune(text)
	if len(runes) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(runes) {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
		start = end - chunkOverlap
	}
	return chunks
}
