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

// AIPerformanceStats reads from ai_responses, a table this codebase's
// schema defines but whose write path (the actual AI reply pipeline) has
// not been built yet — see docs/DASHBOARD_MILESTONE.md. Until that
// pipeline exists and writes rows, TotalResponses will be 0 and the
// pointer fields nil. This is a real, honest query against real (empty)
// data, not a mock.
type AIPerformanceStats struct {
	TotalResponses int64
	AvgConfidence  *float64 // 0-1, nil if TotalResponses == 0
	AvgLatencyMs   *float64
	HandoffRate    *float64 // 0-1 share of responses that triggered human handoff
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
