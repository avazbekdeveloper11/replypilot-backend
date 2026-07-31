package entity

import (
	"time"

	"github.com/google/uuid"
)

// AIResponse is one record of the AI reply pipeline actually generating and
// sending a reply — created only when a reply was sent, not for every
// inbound message. See internal/usecase/ai's doc comment for why a
// low-confidence "no reply, hand off to human" outcome does not create a
// row here (there is no message for it to attach to — MessageID is a NOT
// NULL composite FK into the partitioned messages table, see
// migrations/000001's trade-off note).
type AIResponse struct {
	ID                  uuid.UUID
	OrganizationID      uuid.UUID
	ConversationID      uuid.UUID
	MessageID           uuid.UUID // the OUTBOUND message this response produced
	MessageCreatedAt    time.Time
	ModelUsed           string
	PromptTokens        int
	CompletionTokens    int
	// ConfidenceScore is a heuristic proxy (the top RAG-retrieved chunk's
	// cosine similarity), not a value Gemini's generateContent API reports —
	// see internal/usecase/ai's doc comment.
	ConfidenceScore     *float64
	WasHandoffTriggered bool
	LatencyMs           *int
	CreatedAt           time.Time
}

// AIResponseCitation is the audit trail for exactly which knowledge-base
// chunks grounded one AIResponse.
type AIResponseCitation struct {
	AIResponseID     uuid.UUID
	KnowledgeChunkID uuid.UUID
	OrganizationID   uuid.UUID
	SimilarityScore  *float64
}
