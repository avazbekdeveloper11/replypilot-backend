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
	Status         KBDocumentStatus
	ErrorMessage   *string
	UploadedBy     *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}
