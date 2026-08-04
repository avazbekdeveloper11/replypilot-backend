// Package product is the CRUD behind an organization's own sellable-item
// catalog — see entity.Product's doc comment for why this exists (the AI
// reply pipeline needs a structured price list, not another RAG document).
package product

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

const defaultCurrency = "UZS"

type UseCase struct {
	repo repository.ProductRepository
}

func New(repo repository.ProductRepository) *UseCase {
	return &UseCase{repo: repo}
}

type CreateInput struct {
	OrganizationID uuid.UUID
	Name           string
	Description    *string
	PriceCents     int64
	Currency       string
}

func (uc *UseCase) Create(ctx context.Context, in CreateInput) (*entity.Product, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperror.InvalidInput("product name is required", nil)
	}
	if in.PriceCents < 0 {
		return nil, apperror.InvalidInput("price cannot be negative", nil)
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}

	p := &entity.Product{
		OrganizationID: in.OrganizationID,
		Name:           name,
		Description:    in.Description,
		PriceCents:     in.PriceCents,
		Currency:       currency,
		IsActive:       true,
	}
	if err := uc.repo.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (uc *UseCase) List(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error) {
	return uc.repo.ListByOrganization(ctx, orgID)
}

func (uc *UseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*entity.Product, error) {
	return uc.repo.FindByID(ctx, orgID, id)
}

type UpdateInput struct {
	OrganizationID uuid.UUID
	ID             uuid.UUID
	Name           string
	Description    *string
	PriceCents     int64
	Currency       string
	IsActive       bool
}

func (uc *UseCase) Update(ctx context.Context, in UpdateInput) (*entity.Product, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, apperror.InvalidInput("product name is required", nil)
	}
	if in.PriceCents < 0 {
		return nil, apperror.InvalidInput("price cannot be negative", nil)
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}

	existing, err := uc.repo.FindByID(ctx, in.OrganizationID, in.ID)
	if err != nil {
		return nil, err
	}
	existing.Name = name
	existing.Description = in.Description
	existing.PriceCents = in.PriceCents
	existing.Currency = currency
	existing.IsActive = in.IsActive

	if err := uc.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

func (uc *UseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.repo.Delete(ctx, orgID, id)
}
