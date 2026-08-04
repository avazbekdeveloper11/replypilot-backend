package entity

import (
	"time"

	"github.com/google/uuid"
)

type KBSourceType string

const (
	KBSourceTypeFile       KBSourceType = "file"
	KBSourceTypeURL        KBSourceType = "url"
	KBSourceTypeManualText KBSourceType = "manual_text"
	KBSourceTypeFAQ        KBSourceType = "faq"
)

type KBDocumentStatus string

const (
	KBDocumentStatusPending    KBDocumentStatus = "pending"
	KBDocumentStatusProcessing KBDocumentStatus = "processing"
	KBDocumentStatusReady      KBDocumentStatus = "ready"
	KBDocumentStatusFailed     KBDocumentStatus = "failed"
)

// KnowledgeDocument is one source the AI reply pipeline can draw on —
// currently only KBSourceTypeManualText (pasted text) and
// KBSourceTypeFile (a plain .txt/.md upload, read as UTF-8 text) are
// actually ingested; KBSourceTypeURL and KBSourceTypeFAQ exist in the
// schema/enum but have no usecase code behind them yet — see
// docs/KNOWLEDGE_BASE_MILESTONE.md.
type KnowledgeDocument struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Title          string
	SourceType     KBSourceType
	FileURL        *string
	// Content is the raw source text ingestion was run on — pasted text
	// as-is, or a .txt/.md file's decoded contents. Persisted (added in
	// migration 000012) specifically so the Knowledge Base page can offer
	// an edit flow: without this, the only durable copy of a document's
	// text was its chunked+embedded pieces in knowledge_base_chunks, which
	// overlap by design (see knowledgebase.chunkOverlap) and so can't be
	// cleanly reassembled back into an editable original. Nil for any
	// document created before this column existed, until it's re-saved.
	Content      *string
	Status       KBDocumentStatus
	ErrorMessage *string
	UploadedBy   *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}
