package entity

import (
	"time"

	"github.com/google/uuid"
)

// AIInsights is one organization's cached, on-demand AI-generated overview
// — the org-wide counterpart to Conversation.AISummary. SalesCount/
// SalesAmountCents come from a real aggregate query over paid orders (see
// repository.OrderStats), not from Gemini — Gemini only narrates them
// alongside a qualitative read of recent customer messages (common themes,
// sentiment). See internal/usecase/insights.UseCase.Regenerate for exactly
// how Summary is built.
//
// At most one row per organization, overwritten in place on every
// regenerate — same shape as ClickIntegration, not an append-only history.
type AIInsights struct {
	OrganizationID    uuid.UUID
	Summary           string
	SalesCount        int
	SalesAmountCents  int64
	LeadCount         int
	ConversationCount int
	GeneratedAt       time.Time
}
