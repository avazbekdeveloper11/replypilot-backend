package repository

import (
	"context"

	"github.com/google/uuid"
)

// ResponseTimePerDay is one day's average first-response time — nil
// (not zero) for a day with no conversation that had both an inbound
// message and a subsequent outbound reply, same "nil means no data" rule
// as DashboardRepository.AvgFirstResponseSeconds.
type ResponseTimePerDay struct {
	Date       string // YYYY-MM-DD, UTC
	AvgSeconds *float64
}

// AIUsagePerDay is one day's AI reply pipeline activity — response count,
// total Gemini tokens consumed (prompt+completion), and the handoff count
// for conversations that never reached generation (see
// internal/usecase/ai's package doc comment on why a pre-generation
// handoff has no ai_responses row and so cannot be counted here — this is
// AI RESPONSES sent, not "messages that needed a human").
type AIUsagePerDay struct {
	Date          string
	ResponseCount int64
	TotalTokens   int64
	AvgConfidence *float64
}

// ConversationOutcomes is a snapshot count of conversations by status —
// deliberately a separate query from DashboardRepository.ConversationStats
// even though they overlap, because Analytics presents this as a
// resolution-rate breakdown (chart-shaped: one row per status) rather than
// the Dashboard's flat stat cards; duplicating a `GROUP BY status` query is
// cheaper and clearer than threading a shared struct across two unrelated
// pages for a 6-row aggregate.
type ConversationOutcomes struct {
	AIActive     int64
	PendingHuman int64
	HumanActive  int64
	Resolved     int64
	Closed       int64
}

// AnalyticsRepository is read-only aggregate queries backing the Analytics
// page — same "computed view, not a domain entity" shape as
// DashboardRepository. Scope note: this codebase has no Leads
// entity/repository/usecase built yet (see backend/README.md), so there is
// no conversion-funnel (lead created -> qualified -> converted) query
// here — only conversation/AI-pipeline aggregates, which is everything
// that actually has data behind it today.
type AnalyticsRepository interface {
	ResponseTimePerDay(ctx context.Context, orgID uuid.UUID, days int) ([]ResponseTimePerDay, error)
	AIUsagePerDay(ctx context.Context, orgID uuid.UUID, days int) ([]AIUsagePerDay, error)
	ConversationOutcomes(ctx context.Context, orgID uuid.UUID) (*ConversationOutcomes, error)
}
