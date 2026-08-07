package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

const defaultConversationPageSize = 20

// defaultBroadcastCandidateLimit mirrors this codebase's "MVP-sized shop"
// capping convention (see internal/usecase/insights' maxSampledMessages) —
// campaign.UseCase.Draft caps how many conversations one instruction can
// ever resolve to, so a vague instruction on a large org can't accidentally
// draft a campaign to tens of thousands of people in one call.
const defaultBroadcastCandidateLimit = 500

type ConversationRepository struct {
	db *gorm.DB
}

func NewConversationRepository(db *gorm.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

func (r *ConversationRepository) Create(ctx context.Context, conv *entity.Conversation) error {
	model := conversationToModel(conv)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, conv.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create conversation", err)
	}

	*conv = *modelToConversation(model)
	return nil
}

func (r *ConversationRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Conversation, error) {
	var model ConversationModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id = ? AND organization_id = ?", id, orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("conversation not found")
		}
		return nil, apperror.Internal("find conversation by id", err)
	}
	return modelToConversation(&model), nil
}

func (r *ConversationRepository) FindByAccountAndCustomer(ctx context.Context, orgID, instagramAccountID uuid.UUID, customerIGID string) (*entity.Conversation, error) {
	var model ConversationModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("instagram_account_id = ? AND customer_ig_id = ?", instagramAccountID, customerIGID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("conversation not found")
		}
		return nil, apperror.Internal("find conversation by account and customer", err)
	}
	return modelToConversation(&model), nil
}

// FindByTelegramAccountAndCustomer is FindByAccountAndCustomer's
// Telegram-channel counterpart — see the interface doc comment.
func (r *ConversationRepository) FindByTelegramAccountAndCustomer(ctx context.Context, orgID, telegramAccountID uuid.UUID, customerChatID string) (*entity.Conversation, error) {
	var model ConversationModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("telegram_account_id = ? AND customer_ig_id = ?", telegramAccountID, customerChatID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("conversation not found")
		}
		return nil, apperror.Internal("find conversation by telegram account and customer", err)
	}
	return modelToConversation(&model), nil
}

// List returns conversations newest-first using keyset (cursor) pagination
// on last_message_at — not OFFSET, which degrades badly at scale. Pass the
// last_message_at of the last row from the previous page as CursorBefore to
// fetch the next page.
func (r *ConversationRepository) List(ctx context.Context, params repository.ConversationListParams) ([]*entity.Conversation, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = defaultConversationPageSize
	}

	var models []ConversationModel
	err := withTenant(ctx, r.db, params.OrganizationID, func(tx *gorm.DB) error {
		query := tx.
			Where("organization_id = ?", params.OrganizationID).
			Order("last_message_at DESC NULLS LAST").
			Limit(limit)

		if params.Status != nil {
			query = query.Where("status = ?", string(*params.Status))
		}
		if params.Search != "" {
			query = query.Where("customer_username ILIKE ?", "%"+params.Search+"%")
		}
		if params.CursorBefore != nil {
			query = query.Where("last_message_at < ?", *params.CursorBefore)
		}

		return query.Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list conversations", err)
	}

	conversations := make([]*entity.Conversation, 0, len(models))
	for i := range models {
		conversations = append(conversations, modelToConversation(&models[i]))
	}
	return conversations, nil
}

func (r *ConversationRepository) Update(ctx context.Context, conv *entity.Conversation) error {
	model := conversationToModel(conv)
	var rowsAffected int64
	err := withTenant(ctx, r.db, conv.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&ConversationModel{}).Where("id = ?", conv.ID).Updates(model)
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update conversation", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("conversation not found")
	}
	return nil
}

// ListBroadcastCandidates resolves campaign.UseCase.Draft's segment — see
// the interface doc comment. Two queries, not one: the first (raw SQL, a
// LATERAL join) computes the one thing no existing method exposes — each
// conversation's last CUSTOMER-inbound message time, as opposed to
// conversations.last_message_at which bumps on outbound sends too — plus
// whether a paid order exists, then the second reuses the ordinary
// tenant-scoped Find+modelToConversation path for the matched rows. Kept as
// two round trips rather than one giant SELECT with every ConversationModel
// column duplicated into the raw query by hand, which would silently drift
// out of sync the next time a column is added to conversations.
func (r *ConversationRepository) ListBroadcastCandidates(ctx context.Context, orgID uuid.UUID, minDaysAgo int, maxDaysAgo *int, channel *entity.ConversationChannel, limit int) ([]*repository.BroadcastCandidate, error) {
	if limit <= 0 || limit > defaultBroadcastCandidateLimit {
		limit = defaultBroadcastCandidateLimit
	}

	query := `
		SELECT c.id AS conversation_id,
		       lm.last_customer_message_at AS last_customer_message_at,
		       EXISTS(SELECT 1 FROM orders o WHERE o.conversation_id = c.id AND o.status = 'paid') AS has_paid_order
		FROM conversations c
		JOIN LATERAL (
			SELECT MAX(m.created_at) AS last_customer_message_at
			FROM messages m
			WHERE m.conversation_id = c.id AND m.direction = 'inbound' AND m.sender_type = 'customer'
		) lm ON true
		WHERE c.organization_id = ?
		  AND c.deleted_at IS NULL
		  AND lm.last_customer_message_at IS NOT NULL
		  AND lm.last_customer_message_at <= now() - make_interval(days => ?)`
	args := []any{orgID, minDaysAgo}

	if maxDaysAgo != nil {
		query += ` AND lm.last_customer_message_at >= now() - make_interval(days => ?)`
		args = append(args, *maxDaysAgo)
	}
	if channel != nil {
		query += ` AND c.channel = ?`
		args = append(args, string(*channel))
	}
	query += ` ORDER BY lm.last_customer_message_at DESC LIMIT ?`
	args = append(args, limit)

	var rows []struct {
		ConversationID        uuid.UUID `gorm:"column:conversation_id"`
		LastCustomerMessageAt time.Time `gorm:"column:last_customer_message_at"`
		HasPaidOrder          bool      `gorm:"column:has_paid_order"`
	}
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Raw(query, args...).Scan(&rows).Error
	})
	if err != nil {
		return nil, apperror.Internal("list broadcast candidates", err)
	}
	if len(rows) == 0 {
		return []*repository.BroadcastCandidate{}, nil
	}

	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ConversationID
	}
	var models []ConversationModel
	err = withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id IN ?", ids).Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("load broadcast candidate conversations", err)
	}
	byID := make(map[uuid.UUID]*ConversationModel, len(models))
	for i := range models {
		byID[models[i].ID] = &models[i]
	}

	candidates := make([]*repository.BroadcastCandidate, 0, len(rows))
	for _, row := range rows {
		model, ok := byID[row.ConversationID]
		if !ok {
			// Deleted or reassigned between the two queries — skip rather
			// than fail the whole draft over one stale row.
			continue
		}
		candidates = append(candidates, &repository.BroadcastCandidate{
			Conversation:          modelToConversation(model),
			LastCustomerMessageAt: row.LastCustomerMessageAt,
			HasPaidOrder:          row.HasPaidOrder,
		})
	}
	return candidates, nil
}

func conversationToModel(c *entity.Conversation) *ConversationModel {
	channel := c.Channel
	if channel == "" {
		// Every existing call site sets Channel explicitly now (see
		// instagram.WebhookUseCase.ingestMessage and
		// telegram.WebhookUseCase.ingestMessage), but default defensively
		// to instagram rather than let a zero-value Channel try to insert
		// '' into the channel enum column and fail outright.
		channel = entity.ConversationChannelInstagram
	}
	model := &ConversationModel{
		ID:                   c.ID,
		OrganizationID:       c.OrganizationID,
		TelegramAccountID:    c.TelegramAccountID,
		Channel:              string(channel),
		CustomerIGID:         c.CustomerIGID,
		CustomerUsername:     c.CustomerUsername,
		Status:               string(c.Status),
		AssignedUserID:       c.AssignedUserID,
		LastMessageAt:        c.LastMessageAt,
		LastMessagePreview:   c.LastMessagePreview,
		UnreadCount:          c.UnreadCount,
		AISummary:            c.AISummary,
		AISummaryGeneratedAt: c.AISummaryGeneratedAt,
	}
	// InstagramAccountID must reach the DB as NULL (not the zero UUID) on a
	// Telegram-channel row — chk_conversations_channel_account requires it.
	if channel == entity.ConversationChannelInstagram {
		id := c.InstagramAccountID
		model.InstagramAccountID = &id
	}
	return model
}

func modelToConversation(m *ConversationModel) *entity.Conversation {
	e := &entity.Conversation{
		ID:                   m.ID,
		OrganizationID:       m.OrganizationID,
		Channel:              entity.ConversationChannel(m.Channel),
		TelegramAccountID:    m.TelegramAccountID,
		CustomerIGID:         m.CustomerIGID,
		CustomerUsername:     m.CustomerUsername,
		Status:               entity.ConversationStatus(m.Status),
		AssignedUserID:       m.AssignedUserID,
		LastMessageAt:        m.LastMessageAt,
		LastMessagePreview:   m.LastMessagePreview,
		UnreadCount:          m.UnreadCount,
		AISummary:            m.AISummary,
		AISummaryGeneratedAt: m.AISummaryGeneratedAt,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}
	if m.InstagramAccountID != nil {
		e.InstagramAccountID = *m.InstagramAccountID
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
