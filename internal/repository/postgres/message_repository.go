package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

const defaultMessagePageSize = 50

type MessageRepository struct {
	db *gorm.DB
}

func NewMessageRepository(db *gorm.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

func (r *MessageRepository) Create(ctx context.Context, msg *entity.Message) error {
	model, err := messageToModel(msg)
	if err != nil {
		return apperror.InvalidInput("marshal message metadata", err)
	}
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}
	if model.CreatedAt.IsZero() {
		model.CreatedAt = time.Now()
	}

	err = withTenant(ctx, r.db, msg.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create message", err)
	}

	converted, err := modelToMessage(model)
	if err != nil {
		return apperror.Internal("unmarshal message metadata", err)
	}
	*msg = *converted
	return nil
}

func (r *MessageRepository) FindByIGMessageID(ctx context.Context, orgID uuid.UUID, igMessageID string) (*entity.Message, error) {
	var model MessageModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ? AND ig_message_id = ?", orgID, igMessageID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("message not found")
		}
		return nil, apperror.Internal("find message by ig message id", err)
	}
	return modelToMessage(&model)
}

// List returns messages newest-first using keyset pagination on created_at
// — required, not optional, on a partitioned table this size: an OFFSET
// scan would have to walk and discard rows across partition boundaries.
func (r *MessageRepository) List(ctx context.Context, params repository.MessageListParams) ([]*entity.Message, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = defaultMessagePageSize
	}

	var models []MessageModel
	err := withTenant(ctx, r.db, params.OrganizationID, func(tx *gorm.DB) error {
		query := tx.
			Where("organization_id = ? AND conversation_id = ?", params.OrganizationID, params.ConversationID).
			Order("created_at DESC").
			Limit(limit)

		if params.CursorBefore != nil {
			query = query.Where("created_at < ?", *params.CursorBefore)
		}

		return query.Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list messages", err)
	}

	messages := make([]*entity.Message, 0, len(models))
	for i := range models {
		m, err := modelToMessage(&models[i])
		if err != nil {
			return nil, apperror.Internal("unmarshal message metadata", err)
		}
		messages = append(messages, m)
	}
	return messages, nil
}

// ListRecentInboundByOrganization backs internal/usecase/insights' org-wide
// sentiment/theme synthesis — see the interface doc comment. Queries across
// the whole (partitioned-by-created_at) messages table rather than one
// conversation, newest first, capped at limit — Postgres partition pruning
// keeps this cheap even as the table grows, since ORDER BY created_at DESC
// LIMIT N only has to touch the most recent partitions.
func (r *MessageRepository) ListRecentInboundByOrganization(ctx context.Context, orgID uuid.UUID, limit int) ([]*entity.Message, error) {
	if limit <= 0 {
		limit = defaultMessagePageSize
	}

	var models []MessageModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.
			Where("organization_id = ? AND direction = ? AND sender_type = ?", orgID, string(entity.MessageDirectionInbound), string(entity.MessageSenderCustomer)).
			Order("created_at DESC").
			Limit(limit).
			Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list recent inbound messages", err)
	}

	messages := make([]*entity.Message, 0, len(models))
	for i := range models {
		m, err := modelToMessage(&models[i])
		if err != nil {
			return nil, apperror.Internal("unmarshal message metadata", err)
		}
		messages = append(messages, m)
	}
	return messages, nil
}

// LastCustomerMessageAt backs campaign.UseCase.Send's point-in-time
// eligibility re-check — see the interface doc comment for why this exists
// separately from the cached value ListBroadcastCandidates already
// computed for Draft. A plain scalar MAX query, not a LATERAL join, since
// this is always scoped to one already-known conversation.
func (r *MessageRepository) LastCustomerMessageAt(ctx context.Context, orgID, conversationID uuid.UUID) (*time.Time, error) {
	var result struct {
		MaxCreatedAt *time.Time
	}
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Model(&MessageModel{}).
			Select("max(created_at) as max_created_at").
			Where("organization_id = ? AND conversation_id = ? AND direction = ? AND sender_type = ?",
				orgID, conversationID, string(entity.MessageDirectionInbound), string(entity.MessageSenderCustomer)).
			Scan(&result).Error
	})
	if err != nil {
		return nil, apperror.Internal("last customer message at", err)
	}
	return result.MaxCreatedAt, nil
}

func messageToModel(msg *entity.Message) (*MessageModel, error) {
	var metadataJSON []byte
	if msg.Metadata != nil {
		b, err := json.Marshal(msg.Metadata)
		if err != nil {
			return nil, err
		}
		metadataJSON = b
	} else {
		metadataJSON = []byte("{}")
	}

	return &MessageModel{
		ID:             msg.ID,
		OrganizationID: msg.OrganizationID,
		ConversationID: msg.ConversationID,
		Direction:      string(msg.Direction),
		SenderType:     string(msg.SenderType),
		SenderUserID:   msg.SenderUserID,
		MessageType:    string(msg.MessageType),
		Content:        msg.Content,
		AttachmentURL:  msg.AttachmentURL,
		IGMessageID:    msg.IGMessageID,
		Metadata:       metadataJSON,
		CreatedAt:      msg.CreatedAt,
		DeletedAt:      msg.DeletedAt,
	}, nil
}

func modelToMessage(m *MessageModel) (*entity.Message, error) {
	metadata := map[string]any{}
	if len(m.Metadata) > 0 {
		if err := json.Unmarshal(m.Metadata, &metadata); err != nil {
			return nil, err
		}
	}

	return &entity.Message{
		ID:             m.ID,
		OrganizationID: m.OrganizationID,
		ConversationID: m.ConversationID,
		Direction:      entity.MessageDirection(m.Direction),
		SenderType:     entity.MessageSenderType(m.SenderType),
		SenderUserID:   m.SenderUserID,
		MessageType:    entity.MessageType(m.MessageType),
		Content:        m.Content,
		AttachmentURL:  m.AttachmentURL,
		IGMessageID:    m.IGMessageID,
		Metadata:       metadata,
		CreatedAt:      m.CreatedAt,
		DeletedAt:      m.DeletedAt,
	}, nil
}
