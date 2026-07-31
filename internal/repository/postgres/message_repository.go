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
