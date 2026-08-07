package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ConversationStats is a snapshot count of conversations by status for one
// organization, plus a couple of adjacent counts (unread, messages sent
// today) that the Dashboard's Statistics Cards need alongside it. Computed
// with GROUP BY / COUNT queries, not cached — organizations at this
// product's scale (single-digit thousands of conversations) don't need a
// materialized rollup yet; add one if this ever shows up in a slow query
// log.
type ConversationStats struct {
	Total         int64
	AIActive      int64
	PendingHuman  int64
	HumanActive   int64
	Resolved      int64
	Closed        int64
	Unread        int64
	MessagesToday int64
}

// ConversationsPerDay is one bucket of a daily time series, always
// zero-filled for days with no activity by the caller (see
// DashboardRepository.ConversationsPerDay) so chart libraries get a
// continuous x-axis instead of gaps.
type ConversationsPerDay struct {
	Date  string // YYYY-MM-DD, UTC
	Count int64
}

// AIPerformanceStats reads from ai_responses, which usecase/ai.UseCase
// writes a row to on every AI-generated reply. TotalResponses is 0 and the
// pointer fields nil only for an org that hasn't had the AI handle a
// message yet, not because the pipeline is unbuilt.
type AIPerformanceStats struct {
	TotalResponses int64
	AvgConfidence  *float64 // 0-1, nil if TotalResponses == 0
	AvgLatencyMs   *float64
	HandoffRate    *float64 // 0-1 share of responses that triggered human handoff
	// TotalLatencyMs is the sum (not average) of every ai_responses.latency_ms
	// row — "how much wall-clock time the AI has actually spent generating
	// replies", all-time. Shown on the Dashboard instead of avg-first-response
	// time, which is dragged up by conversations waiting on a human handoff
	// and so isn't a fair reflection of the AI's own speed.
	TotalLatencyMs *float64
}

// DashboardRepository is read-only aggregate queries backing the Dashboard
// page. Unlike the other repositories in this package it has no Create/
// Update — nothing in the domain model is "a dashboard row"; these are
// computed views over conversations, messages, and ai_responses.
type DashboardRepository interface {
	ConversationStats(ctx context.Context, orgID uuid.UUID) (*ConversationStats, error)
	ConversationsPerDay(ctx context.Context, orgID uuid.UUID, days int) ([]ConversationsPerDay, error)
	// AvgFirstResponseSeconds returns nil (not zero) when no conversation
	// in the window has both an inbound message and a subsequent outbound
	// reply yet — a nil average is not the same claim as "0 seconds".
	AvgFirstResponseSeconds(ctx context.Context, orgID uuid.UUID, since time.Time) (*float64, error)
	AIPerformance(ctx context.Context, orgID uuid.UUID) (*AIPerformanceStats, error)
}
