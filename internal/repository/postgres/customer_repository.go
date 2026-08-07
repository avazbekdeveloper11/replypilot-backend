package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

// defaultCustomerListLimit mirrors this codebase's "MVP-sized shop"
// capping convention (see postgres.defaultBroadcastCandidateLimit) — the
// customer database shows the org's biggest/most-recent customers first,
// capped, rather than every conversation ever created.
const defaultCustomerListLimit = 500

type CustomerRepository struct {
	db *gorm.DB
}

func NewCustomerRepository(db *gorm.DB) *CustomerRepository {
	return &CustomerRepository{db: db}
}

// ListSummaries is a single aggregate query (LEFT JOIN, not INNER — a
// customer with zero orders still belongs in the list, e.g. because
// they've never been nudged toward a purchase yet, not just customers who
// already bought) grouped per conversation. See the interface doc comment
// for the ordering/pagination rationale.
func (r *CustomerRepository) ListSummaries(ctx context.Context, params repository.CustomerListParams) ([]*entity.CustomerSummary, error) {
	limit := params.Limit
	if limit <= 0 || limit > defaultCustomerListLimit {
		limit = defaultCustomerListLimit
	}

	query := `
		SELECT c.id AS conversation_id,
		       c.channel AS channel,
		       c.customer_username AS customer_username,
		       c.last_message_at AS last_message_at,
		       COALESCE(SUM(CASE WHEN o.status = 'paid' THEN o.amount_cents ELSE 0 END), 0) AS total_paid_cents,
		       COUNT(CASE WHEN o.status = 'paid' THEN 1 END) AS paid_order_count,
		       MAX(CASE WHEN o.status = 'paid' THEN o.paid_at END) AS last_paid_at
		FROM conversations c
		LEFT JOIN orders o ON o.conversation_id = c.id
		WHERE c.organization_id = ? AND c.deleted_at IS NULL`
	args := []any{params.OrganizationID}

	if params.Search != "" {
		query += ` AND c.customer_username ILIKE ?`
		args = append(args, "%"+params.Search+"%")
	}

	query += `
		GROUP BY c.id, c.channel, c.customer_username, c.last_message_at
		ORDER BY total_paid_cents DESC, c.last_message_at DESC NULLS LAST
		LIMIT ?`
	args = append(args, limit)

	var rows []struct {
		ConversationID   uuid.UUID  `gorm:"column:conversation_id"`
		Channel          string     `gorm:"column:channel"`
		CustomerUsername *string    `gorm:"column:customer_username"`
		LastMessageAt    *time.Time `gorm:"column:last_message_at"`
		TotalPaidCents   int64      `gorm:"column:total_paid_cents"`
		PaidOrderCount   int        `gorm:"column:paid_order_count"`
		LastPaidAt       *time.Time `gorm:"column:last_paid_at"`
	}
	err := withTenant(ctx, r.db, params.OrganizationID, func(tx *gorm.DB) error {
		return tx.Raw(query, args...).Scan(&rows).Error
	})
	if err != nil {
		return nil, apperror.Internal("list customer summaries", err)
	}

	summaries := make([]*entity.CustomerSummary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, &entity.CustomerSummary{
			ConversationID:   row.ConversationID,
			Channel:          entity.ConversationChannel(row.Channel),
			CustomerUsername: row.CustomerUsername,
			LastMessageAt:    row.LastMessageAt,
			TotalPaidCents:   row.TotalPaidCents,
			PaidOrderCount:   row.PaidOrderCount,
			LastPaidAt:       row.LastPaidAt,
		})
	}
	return summaries, nil
}
