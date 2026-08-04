package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type LeadRepository struct {
	db *gorm.DB
}

func NewLeadRepository(db *gorm.DB) *LeadRepository {
	return &LeadRepository{db: db}
}

func (r *LeadRepository) Create(ctx context.Context, lead *entity.Lead) error {
	model := leadToModel(lead)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}
	if model.Status == "" {
		model.Status = string(entity.LeadStatusNew)
	}

	err := withTenant(ctx, r.db, lead.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create lead", err)
	}

	*lead = *modelToLead(model)
	return nil
}

func (r *LeadRepository) HasOpen(ctx context.Context, orgID, conversationID uuid.UUID) (bool, error) {
	var count int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Model(&LeadModel{}).
			Where("organization_id = ? AND conversation_id = ? AND status = ?", orgID, conversationID, string(entity.LeadStatusNew)).
			Count(&count).Error
	})
	if err != nil {
		return false, apperror.Internal("check open lead", err)
	}
	return count > 0, nil
}

// leadListRow is a flattened scan target for the leads<->conversations
// join — see entity.Lead.CustomerUsername's doc comment for why this
// isn't just LeadModel.
type leadListRow struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	ConversationID   uuid.UUID
	Phone            string
	Summary          string
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CustomerUsername *string
}

func (r *LeadRepository) ListByOrganization(ctx context.Context, orgID uuid.UUID, status *entity.LeadStatus) ([]*entity.Lead, error) {
	var rows []leadListRow
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		query := tx.Table("leads").
			Select("leads.id, leads.organization_id, leads.conversation_id, leads.phone, leads.summary, leads.status, leads.created_at, leads.updated_at, conversations.customer_username").
			Joins("LEFT JOIN conversations ON conversations.id = leads.conversation_id").
			Where("leads.organization_id = ?", orgID).
			Order("leads.created_at DESC")
		if status != nil {
			query = query.Where("leads.status = ?", string(*status))
		}
		return query.Scan(&rows).Error
	})
	if err != nil {
		return nil, apperror.Internal("list leads", err)
	}

	leads := make([]*entity.Lead, 0, len(rows))
	for _, row := range rows {
		leads = append(leads, &entity.Lead{
			ID:               row.ID,
			OrganizationID:   row.OrganizationID,
			ConversationID:   row.ConversationID,
			Phone:            row.Phone,
			Summary:          row.Summary,
			Status:           entity.LeadStatus(row.Status),
			CustomerUsername: row.CustomerUsername,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
		})
	}
	return leads, nil
}

func (r *LeadRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Lead, error) {
	var row leadListRow
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Table("leads").
			Select("leads.id, leads.organization_id, leads.conversation_id, leads.phone, leads.summary, leads.status, leads.created_at, leads.updated_at, conversations.customer_username").
			Joins("LEFT JOIN conversations ON conversations.id = leads.conversation_id").
			Where("leads.id = ? AND leads.organization_id = ?", id, orgID).
			Take(&row).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("lead not found")
		}
		return nil, apperror.Internal("find lead by id", err)
	}
	return &entity.Lead{
		ID:               row.ID,
		OrganizationID:   row.OrganizationID,
		ConversationID:   row.ConversationID,
		Phone:            row.Phone,
		Summary:          row.Summary,
		Status:           entity.LeadStatus(row.Status),
		CustomerUsername: row.CustomerUsername,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}, nil
}

func (r *LeadRepository) UpdateStatus(ctx context.Context, orgID, id uuid.UUID, status entity.LeadStatus) (*entity.Lead, error) {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Model(&LeadModel{}).
			Where("id = ? AND organization_id = ?", id, orgID).
			Update("status", string(status))
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return nil, apperror.Internal("update lead status", err)
	}
	if rowsAffected == 0 {
		return nil, apperror.NotFound("lead not found")
	}
	return r.FindByID(ctx, orgID, id)
}

func leadToModel(l *entity.Lead) *LeadModel {
	return &LeadModel{
		ID:             l.ID,
		OrganizationID: l.OrganizationID,
		ConversationID: l.ConversationID,
		Phone:          l.Phone,
		Summary:        l.Summary,
		Status:         string(l.Status),
	}
}

func modelToLead(m *LeadModel) *entity.Lead {
	return &entity.Lead{
		ID:             m.ID,
		OrganizationID: m.OrganizationID,
		ConversationID: m.ConversationID,
		Phone:          m.Phone,
		Summary:        m.Summary,
		Status:         entity.LeadStatus(m.Status),
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
