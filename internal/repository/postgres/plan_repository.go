package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
)

// PlanRepository queries plain, unscoped SQL — no withTenant, since plans
// has no organization_id / RLS policy (it's global reference data, see
// model.go's doc comment on PlanModel).
type PlanRepository struct {
	db *gorm.DB
}

func NewPlanRepository(db *gorm.DB) *PlanRepository {
	return &PlanRepository{db: db}
}

func (r *PlanRepository) ListActive(ctx context.Context) ([]*entity.Plan, error) {
	var models []PlanModel
	if err := r.db.WithContext(ctx).Where("is_active = ?", true).Order("price_monthly_cents ASC").Find(&models).Error; err != nil {
		return nil, apperror.Internal("list active plans", err)
	}
	plans := make([]*entity.Plan, 0, len(models))
	for i := range models {
		p, err := modelToPlan(&models[i])
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, nil
}

func (r *PlanRepository) FindByCode(ctx context.Context, code string) (*entity.Plan, error) {
	var model PlanModel
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("plan not found")
		}
		return nil, apperror.Internal("find plan by code", err)
	}
	return modelToPlan(&model)
}

func (r *PlanRepository) FindByStripePriceID(ctx context.Context, stripePriceID string) (*entity.Plan, error) {
	var model PlanModel
	err := r.db.WithContext(ctx).
		Where("stripe_price_id_monthly = ? OR stripe_price_id_yearly = ?", stripePriceID, stripePriceID).
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("plan not found for stripe price")
		}
		return nil, apperror.Internal("find plan by stripe price id", err)
	}
	return modelToPlan(&model)
}

func (r *PlanRepository) FindByID(ctx context.Context, id uuid.UUID) (*entity.Plan, error) {
	var model PlanModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.NotFound("plan not found")
		}
		return nil, apperror.Internal("find plan by id", err)
	}
	return modelToPlan(&model)
}

func modelToPlan(m *PlanModel) (*entity.Plan, error) {
	features := map[string]any{}
	if len(m.Features) > 0 {
		if err := json.Unmarshal(m.Features, &features); err != nil {
			return nil, apperror.Internal("unmarshal plan features", err)
		}
	}
	return &entity.Plan{
		ID:                   m.ID,
		Code:                 m.Code,
		Name:                 m.Name,
		PriceMonthlyCents:    m.PriceMonthlyCents,
		PriceYearlyCents:     m.PriceYearlyCents,
		MessageLimit:         m.MessageLimit,
		SeatLimit:            m.SeatLimit,
		Features:             features,
		StripePriceIDMonthly: m.StripePriceIDMonthly,
		StripePriceIDYearly:  m.StripePriceIDYearly,
		IsActive:             m.IsActive,
		CreatedAt:            m.CreatedAt,
		UpdatedAt:            m.UpdatedAt,
	}, nil
}
