package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type KnowledgeDocumentRepository struct {
	db *gorm.DB
}

func NewKnowledgeDocumentRepository(db *gorm.DB) *KnowledgeDocumentRepository {
	return &KnowledgeDocumentRepository{db: db}
}

func (r *KnowledgeDocumentRepository) Create(ctx context.Context, doc *entity.KnowledgeDocument) error {
	model := documentToModel(doc)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, doc.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create knowledge document", err)
	}

	*doc = *modelToDocument(model)
	return nil
}

func (r *KnowledgeDocumentRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.KnowledgeDocument, error) {
	var model KnowledgeDocumentModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id = ? AND organization_id = ?", id, orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("document not found")
		}
		return nil, apperror.Internal("find knowledge document by id", err)
	}
	return modelToDocument(&model), nil
}

func (r *KnowledgeDocumentRepository) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.KnowledgeDocument, error) {
	var models []KnowledgeDocumentModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).Order("created_at DESC").Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list knowledge documents", err)
	}

	docs := make([]*entity.KnowledgeDocument, 0, len(models))
	for i := range models {
		docs = append(docs, modelToDocument(&models[i]))
	}
	return docs, nil
}

func (r *KnowledgeDocumentRepository) Update(ctx context.Context, doc *entity.KnowledgeDocument) error {
	model := documentToModel(doc)
	var rowsAffected int64
	err := withTenant(ctx, r.db, doc.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&KnowledgeDocumentModel{}).Where("id = ?", doc.ID).Updates(map[string]any{
			"title":         model.Title,
			"status":        model.Status,
			"error_message": model.ErrorMessage,
		})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update knowledge document", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("document not found")
	}
	return nil
}

func (r *KnowledgeDocumentRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND organization_id = ?", id, orgID).Delete(&KnowledgeDocumentModel{})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("delete knowledge document", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("document not found")
	}
	return nil
}

func documentToModel(d *entity.KnowledgeDocument) *KnowledgeDocumentModel {
	return &KnowledgeDocumentModel{
		ID:             d.ID,
		OrganizationID: d.OrganizationID,
		Title:          d.Title,
		SourceType:     string(d.SourceType),
		FileURL:        d.FileURL,
		Status:         string(d.Status),
		ErrorMessage:   d.ErrorMessage,
		UploadedBy:     d.UploadedBy,
	}
}

func modelToDocument(m *KnowledgeDocumentModel) *entity.KnowledgeDocument {
	e := &entity.KnowledgeDocument{
		ID:             m.ID,
		OrganizationID: m.OrganizationID,
		Title:          m.Title,
		SourceType:     entity.KBSourceType(m.SourceType),
		FileURL:        m.FileURL,
		Status:         entity.KBDocumentStatus(m.Status),
		ErrorMessage:   m.ErrorMessage,
		UploadedBy:     m.UploadedBy,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}
