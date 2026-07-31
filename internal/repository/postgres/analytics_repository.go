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

type AnalyticsRepository struct {
	db *gorm.DB
}

func NewAnalyticsRepository(db *gorm.DB) *AnalyticsRepository {
	return &AnalyticsRepository{db: db}
}

// ResponseTimePerDay: same first-inbound/first-reply pairing as
// DashboardRepository.AvgFirstResponseSeconds, but bucketed by the
// inbound message's day instead of averaged over the whole window — one
// CTE pair, grouped, rather than N single-day queries.
func (r *AnalyticsRepository) ResponseTimePerDay(ctx context.Context, orgID uuid.UUID, days int) ([]repository.ResponseTimePerDay, error) {
	if days <= 0 {
		days = 14
	}
	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	since := todayStart.AddDate(0, 0, -(days - 1))

	var rows []struct {
		Day        time.Time
		AvgSeconds sql.NullFloat64
	}
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
			SELECT
				date_trunc('day', fi.inbound_at) AS day,
				AVG(EXTRACT(EPOCH FROM (fr.reply_at - fi.inbound_at))) AS avg_seconds
			FROM first_inbound fi
			JOIN first_reply fr ON fr.conversation_id = fi.conversation_id
			GROUP BY date_trunc('day', fi.inbound_at)
			ORDER BY day ASC
		`, orgID, since, orgID).Scan(&rows).Error
	})
	if err != nil {
		return nil, apperror.Internal("compute response time per day", err)
	}

	byDate := make(map[string]float64, len(rows))
	for _, row := range rows {
		if row.AvgSeconds.Valid {
			byDate[row.Day.Format("2006-01-02")] = row.AvgSeconds.Float64
		}
	}

	out := make([]repository.ResponseTimePerDay, 0, days)
	for i := 0; i < days; i++ {
		key := since.AddDate(0, 0, i).Format("2006-01-02")
		point := repository.ResponseTimePerDay{Date: key}
		if v, ok := byDate[key]; ok {
			vCopy := v
			point.AvgSeconds = &vCopy
		}
		out = append(out, point)
	}
	return out, nil
}

// AIUsagePerDay buckets ai_responses by day. total_tokens is the
// GENERATED column (prompt_tokens + completion_tokens, see
// migrations/000001) — summed here, not recomputed.
func (r *AnalyticsRepository) AIUsagePerDay(ctx context.Context, orgID uuid.UUID, days int) ([]repository.AIUsagePerDay, error) {
	if days <= 0 {
		days = 14
	}
	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	since := todayStart.AddDate(0, 0, -(days - 1))

	var rows []struct {
		Day           time.Time
		ResponseCount int64
		TotalTokens   sql.NullInt64
		AvgConfidence sql.NullFloat64
	}
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Raw(`
			SELECT
				date_trunc('day', created_at) AS day,
				count(*) AS response_count,
				sum(total_tokens) AS total_tokens,
				avg(confidence_score) AS avg_confidence
			FROM ai_responses
			WHERE organization_id = ? AND created_at >= ?
			GROUP BY date_trunc('day', created_at)
			ORDER BY day ASC
		`, orgID, since).Scan(&rows).Error
	})
	if err != nil {
		return nil, apperror.Internal("compute ai usage per day", err)
	}

	type bucket struct {
		responseCount int64
		totalTokens   int64
		avgConfidence *float64
	}
	byDate := make(map[string]bucket, len(rows))
	for _, row := range rows {
		b := bucket{responseCount: row.ResponseCount}
		if row.TotalTokens.Valid {
			b.totalTokens = row.TotalTokens.Int64
		}
		if row.AvgConfidence.Valid {
			v := row.AvgConfidence.Float64
			b.avgConfidence = &v
		}
		byDate[row.Day.Format("2006-01-02")] = b
	}

	out := make([]repository.AIUsagePerDay, 0, days)
	for i := 0; i < days; i++ {
		key := since.AddDate(0, 0, i).Format("2006-01-02")
		b := byDate[key]
		out = append(out, repository.AIUsagePerDay{
			Date:          key,
			ResponseCount: b.responseCount,
			TotalTokens:   b.totalTokens,
			AvgConfidence: b.avgConfidence,
		})
	}
	return out, nil
}

func (r *AnalyticsRepository) ConversationOutcomes(ctx context.Context, orgID uuid.UUID) (*repository.ConversationOutcomes, error) {
	outcomes := &repository.ConversationOutcomes{}

	// Scan target declared outside the closure — withTenant's callback
	// only returns an error, so the rows it scans into have to live in a
	// variable the closure captures by reference, same pattern as
	// DashboardRepository.ConversationStats's byStatus.
	var byStatus []struct {
		Status string
		Count  int64
	}
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Model(&ConversationModel{}).
			Select("status, count(*) as count").
			Where("organization_id = ?", orgID).
			Group("status").
			Scan(&byStatus).Error
	})
	if err != nil {
		return nil, apperror.Internal("compute conversation outcomes", err)
	}
	for _, row := range byStatus {
		switch entity.ConversationStatus(row.Status) {
		case entity.ConversationStatusAIActive:
			outcomes.AIActive = row.Count
		case entity.ConversationStatusPendingHuman:
			outcomes.PendingHuman = row.Count
		case entity.ConversationStatusHumanActive:
			outcomes.HumanActive = row.Count
		case entity.ConversationStatusResolved:
			outcomes.Resolved = row.Count
		case entity.ConversationStatusClosed:
			outcomes.Closed = row.Count
		}
	}
	return outcomes, nil
}
