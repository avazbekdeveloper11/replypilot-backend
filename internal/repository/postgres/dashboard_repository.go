package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type DashboardRepository struct {
	db *gorm.DB
}

func NewDashboardRepository(db *gorm.DB) *DashboardRepository {
	return &DashboardRepository{db: db}
}

func (r *DashboardRepository) ConversationStats(ctx context.Context, orgID uuid.UUID) (*repository.ConversationStats, error) {
	stats := &repository.ConversationStats{}

	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		var byStatus []struct {
			Status string
			Count  int64
		}
		if err := tx.Model(&ConversationModel{}).
			Select("status, count(*) as count").
			Where("organization_id = ?", orgID).
			Group("status").
			Scan(&byStatus).Error; err != nil {
			return err
		}
		for _, row := range byStatus {
			stats.Total += row.Count
			switch entity.ConversationStatus(row.Status) {
			case entity.ConversationStatusAIActive:
				stats.AIActive = row.Count
			case entity.ConversationStatusPendingHuman:
				stats.PendingHuman = row.Count
			case entity.ConversationStatusHumanActive:
				stats.HumanActive = row.Count
			case entity.ConversationStatusResolved:
				stats.Resolved = row.Count
			case entity.ConversationStatusClosed:
				stats.Closed = row.Count
			}
		}

		if err := tx.Model(&ConversationModel{}).
			Where("organization_id = ? AND unread_count > 0", orgID).
			Count(&stats.Unread).Error; err != nil {
			return err
		}

		todayStart := time.Now().UTC().Truncate(24 * time.Hour)
		if err := tx.Model(&MessageModel{}).
			Where("organization_id = ? AND created_at >= ?", orgID, todayStart).
			Count(&stats.MessagesToday).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, apperror.Internal("compute conversation stats", err)
	}
	return stats, nil
}

// ConversationsPerDay buckets new conversations by day (UTC) over the
// trailing `days` days, INCLUDING today, and zero-fills days with no
// activity — a chart fed gaps instead of zeros draws a misleading line.
func (r *DashboardRepository) ConversationsPerDay(ctx context.Context, orgID uuid.UUID, days int) ([]repository.ConversationsPerDay, error) {
	if days <= 0 {
		days = 7
	}
	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	since := todayStart.AddDate(0, 0, -(days - 1))

	var rows []struct {
		Day   time.Time
		Count int64
	}
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Model(&ConversationModel{}).
			Select("date_trunc('day', created_at) as day, count(*) as count").
			Where("organization_id = ? AND created_at >= ?", orgID, since).
			Group("date_trunc('day', created_at)").
			Order("day ASC").
			Scan(&rows).Error
	})
	if err != nil {
		return nil, apperror.Internal("compute conversations per day", err)
	}

	byDate := make(map[string]int64, len(rows))
	for _, row := range rows {
		byDate[row.Day.Format("2006-01-02")] = row.Count
	}

	out := make([]repository.ConversationsPerDay, 0, days)
	for i := 0; i < days; i++ {
		key := since.AddDate(0, 0, i).Format("2006-01-02")
		out = append(out, repository.ConversationsPerDay{Date: key, Count: byDate[key]})
	}
	return out, nil
}

// AvgFirstResponseSeconds finds, per conversation, the first inbound
// message and the first outbound message that follows it, then averages
// the gap across every conversation in the window that has both. Two
// DISTINCT ON CTEs rather than a correlated subquery per row — this is the
// standard Postgres "first row per group" pattern and lets the planner use
// the existing idx_messages_org_created / idx_messages_conversation_created
// indexes instead of a per-conversation nested loop.
func (r *DashboardRepository) AvgFirstResponseSeconds(ctx context.Context, orgID uuid.UUID, since time.Time) (*float64, error) {
	var result sql.NullFloat64

	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Raw(`
			WITH first_inbound AS (
				SELECT DISTINCT ON (conversation_id)
					conversation_id, created_at AS inbound_at
				FROM messages
				WHERE organization_id = ? AND direction = 'inbound' AND created_at >= ?
				ORDER BY conversation_id, created_at ASC
			),
			first_reply AS (
				SELECT DISTINCT ON (m.conversation_id)
					m.conversation_id, m.created_at AS reply_at
				FROM messages m
				JOIN first_inbound fi ON fi.conversation_id = m.conversation_id
				WHERE m.organization_id = ? AND m.direction = 'outbound' AND m.created_at > fi.inbound_at
				ORDER BY m.conversation_id, m.created_at ASC
			)
			SELECT AVG(EXTRACT(EPOCH FROM (fr.reply_at - fi.inbound_at))) AS result
			FROM first_inbound fi
			JOIN first_reply fr ON fr.conversation_id = fi.conversation_id
		`, orgID, since, orgID).Scan(&result).Error
	})
	if err != nil {
		return nil, apperror.Internal("compute avg first response time", err)
	}
	if !result.Valid {
		return nil, nil
	}
	return &result.Float64, nil
}

// AIPerformance reads directly from ai_responses instead of going through
// a repository/entity pair, because no such pair exists yet — see the doc
// comment on repository.AIPerformanceStats. Populated by usecase/ai.UseCase
// on every AI-generated reply.
func (r *DashboardRepository) AIPerformance(ctx context.Context, orgID uuid.UUID) (*repository.AIPerformanceStats, error) {
	var row struct {
		Total          int64
		AvgConfidence  sql.NullFloat64
		AvgLatencyMs   sql.NullFloat64
		HandoffRate    sql.NullFloat64
		TotalLatencyMs sql.NullFloat64
	}

	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Raw(`
			SELECT
				count(*) AS total,
				avg(confidence_score) AS avg_confidence,
				avg(latency_ms) AS avg_latency_ms,
				(count(*) FILTER (WHERE was_handoff_triggered))::float / NULLIF(count(*), 0) AS handoff_rate,
				sum(latency_ms) AS total_latency_ms
			FROM ai_responses
			WHERE organization_id = ?
		`, orgID).Scan(&row).Error
	})
	if err != nil {
		return nil, apperror.Internal("compute ai performance stats", err)
	}

	stats := &repository.AIPerformanceStats{TotalResponses: row.Total}
	if row.AvgConfidence.Valid {
		stats.AvgConfidence = &row.AvgConfidence.Float64
	}
	if row.AvgLatencyMs.Valid {
		stats.AvgLatencyMs = &row.AvgLatencyMs.Float64
	}
	if row.HandoffRate.Valid {
		stats.HandoffRate = &row.HandoffRate.Float64
	}
	if row.TotalLatencyMs.Valid {
		stats.TotalLatencyMs = &row.TotalLatencyMs.Float64
	}
	return stats, nil
}
