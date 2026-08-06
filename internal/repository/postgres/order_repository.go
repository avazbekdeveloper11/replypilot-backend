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

type OrderRepository struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

func (r *OrderRepository) Create(ctx context.Context, order *entity.Order) error {
	model := orderToModel(order)
	if model.ID == uuid.Nil {
		model.ID = uuid.New()
	}

	err := withTenant(ctx, r.db, order.OrganizationID, func(tx *gorm.DB) error {
		return tx.Create(model).Error
	})
	if err != nil {
		return apperror.Internal("create order", err)
	}

	*order = *modelToOrder(model)
	return nil
}

func (r *OrderRepository) FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Order, error) {
	var model OrderModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("id = ? AND organization_id = ?", id, orgID).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("order not found")
		}
		return nil, apperror.Internal("find order by id", err)
	}
	return modelToOrder(&model), nil
}

func (r *OrderRepository) FindByTransactionParam(ctx context.Context, orgID uuid.UUID, transactionParam string) (*entity.Order, error) {
	var model OrderModel
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Where("organization_id = ? AND click_transaction_param = ?", orgID, transactionParam).First(&model).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("order not found")
		}
		return nil, apperror.Internal("find order by transaction param", err)
	}
	return modelToOrder(&model), nil
}

// Update writes every field via an explicit map, not GORM's struct-based
// Updates — same reasoning as TelegramAccountRepository.Update: struct-based
// Updates silently skips zero-value fields, which would make it impossible
// to ever clear ClickTransID/PaidAt back to their zero values, and status
// transitions here always set every field to its intended final value
// anyway.
func (r *OrderRepository) Update(ctx context.Context, order *entity.Order) error {
	model := orderToModel(order)
	var rowsAffected int64
	err := withTenant(ctx, r.db, order.OrganizationID, func(tx *gorm.DB) error {
		res := tx.Model(&OrderModel{}).Where("id = ?", order.ID).Updates(map[string]any{
			"status":         model.Status,
			"click_trans_id": model.ClickTransID,
			"paid_at":        model.PaidAt,
		})
		rowsAffected = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return apperror.Internal("update order", err)
	}
	if rowsAffected == 0 {
		return apperror.NotFound("order not found")
	}
	return nil
}

// Stats is a real SQL aggregate (COUNT/COALESCE(SUM,0)) over status='paid'
// — see the interface doc comment on why this, not Gemini, is the source
// of truth for sales figures.
func (r *OrderRepository) Stats(ctx context.Context, orgID uuid.UUID) (*repository.OrderStats, error) {
	var row struct {
		PaidCount       int64
		PaidAmountCents int64
	}
	err := withTenant(ctx, r.db, orgID, func(tx *gorm.DB) error {
		return tx.Model(&OrderModel{}).
			Select("count(*) as paid_count, coalesce(sum(amount_cents), 0) as paid_amount_cents").
			Where("organization_id = ? AND status = ?", orgID, string(entity.OrderStatusPaid)).
			Scan(&row).Error
	})
	if err != nil {
		return nil, apperror.Internal("order stats", err)
	}
	return &repository.OrderStats{
		PaidCount:       int(row.PaidCount),
		PaidAmountCents: row.PaidAmountCents,
	}, nil
}

func orderToModel(o *entity.Order) *OrderModel {
	return &OrderModel{
		ID:                    o.ID,
		OrganizationID:        o.OrganizationID,
		ConversationID:        o.ConversationID,
		ProductID:             o.ProductID,
		ProductNameSnapshot:   o.ProductNameSnapshot,
		AmountCents:           o.AmountCents,
		Currency:              o.Currency,
		Status:                string(o.Status),
		ClickTransactionParam: o.ClickTransactionParam,
		ClickTransID:          o.ClickTransID,
		PaidAt:                o.PaidAt,
	}
}

func modelToOrder(m *OrderModel) *entity.Order {
	return &entity.Order{
		ID:                    m.ID,
		OrganizationID:        m.OrganizationID,
		ConversationID:        m.ConversationID,
		ProductID:             m.ProductID,
		ProductNameSnapshot:   m.ProductNameSnapshot,
		AmountCents:           m.AmountCents,
		Currency:              m.Currency,
		Status:                entity.OrderStatus(m.Status),
		ClickTransactionParam: m.ClickTransactionParam,
		ClickTransID:          m.ClickTransID,
		PaidAt:                m.PaidAt,
		CreatedAt:             m.CreatedAt,
		UpdatedAt:             m.UpdatedAt,
	}
}
