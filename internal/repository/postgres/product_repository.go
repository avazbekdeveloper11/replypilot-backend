package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

type ProductRepository struct {
	db *gorm.DB
}

func NewProductRepository(db *gorm.DB) *ProductRepository {
	return &ProductRepository{db: db}
}

func (r *ProductRepository) Create(ctx context.Context, product *entity.Product) error {
	model := productToModel(product)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, product.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create product", err)
	}

	*product = *modelToProduct(model)
	return nil
}

func (r *ProductRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Product, error) {
	var model ProductModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id = ? AND organization_id = ?", id, orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("product not found")
		}
		return nil, apperror.Internal("find product by id", err)
	}
	return modelToProduct(&model), nil
}

func (r *ProductRepository) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error) {
	var models []ProductModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ?", orgID).Order("created_at DESC").Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list products", err)
	}
	return modelsToProducts(models), nil
}

func (r *ProductRepository) ListActiveByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error) {
	var models []ProductModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ? AND is_active = ?", orgID, true).Order("created_at ASC").Find(&models).Error
	})
	if err != nil {
		return nil, apperror.Internal("list active products", err)
	}
	return modelsToProducts(models), nil
}

func (r *ProductRepository) Update(ctx context.Context, product *entity.Product) error {
	model := productToModel(product)
	var rowsAffected int64
	err := withTenant(ctx, r.db, product.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&ProductModel{}).Where("id = ?", product.ID).Updates(map[string]any{
			"name":        model.Name,
			"description": model.Description,
			"price_cents": model.PriceCents,
			"currency":    model.Currency,
			"is_active":   model.IsActive,
		})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update product", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("product not found")
	}
	return nil
}

func (r *ProductRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	var rowsAffected int64
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND organization_id = ?", id, orgID).Delete(&ProductModel{})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("delete product", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("product not found")
	}
	return nil
}

func productToModel(p *entity.Product) *ProductModel {
	return &ProductModel{
		ID:             p.ID,
		OrganizationID: p.OrganizationID,
		Name:           p.Name,
		Description:    p.Description,
		PriceCents:     p.PriceCents,
		Currency:       p.Currency,
		IsActive:       p.IsActive,
	}
}

func modelToProduct(m *ProductModel) *entity.Product {
	e := &entity.Product{
		ID:             m.ID,
		OrganizationID: m.OrganizationID,
		Name:           m.Name,
		Description:    m.Description,
		PriceCents:     m.PriceCents,
		Currency:       m.Currency,
		IsActive:       m.IsActive,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		e.DeletedAt = &t
	}
	return e
}

func modelsToProducts(models []ProductModel) []*entity.Product {
	products := make([]*entity.Product, 0, len(models))
	for i := range models {
		products = append(products, modelToProduct(&models[i]))
	}
	return products
}
