package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

const defaultConversationPageSize = 20

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

func conversationToModel(c *entity.Conversation) *ConversationModel {
	return &ConversationModel{
		ID:                  c.ID,
		OrganizationID:      c.OrganizationID,
		InstagramAccountID:  c.InstagramAccountID,
		CustomerIGID:        c.CustomerIGID,
		CustomerUsername:    c.CustomerUsername,
		Status:              string(c.Status),
		AssignedUserID:      c.AssignedUserID,
		LastMessageAt:       c.LastMessageAt,
		LastMessagePreview:  c.LastMessagePreview,
		UnreadCount:         c.UnreadCount,
	}
}

func modelToConversation(m *ConversationModel) *entity.Conversation {
	e := &entity.Conversation{
		ID:                 m.ID,
		OrganizationID:     m.OrganizationID,
		InstagramAccountID: m.InstagramAccountID,
		CustomerIGID:       m.CustomerIGID,
		CustomerUsername:   m.CustomerUsername,
		Status:             entity.ConversationStatus(m.Status),
		AssignedUserID:     m.AssignedUserID,
		LastMessageAt:      m.LastMessageAt,
		LastMessagePreview: m.LastMessagePreview,
		UnreadCount:        m.UnreadCount,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
