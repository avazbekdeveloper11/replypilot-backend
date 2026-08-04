package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type ProductRepository interface {
	Create(ctx context.Context, product *entity.Product) error
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Product, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error)
	// ListActiveByOrganization is what internal/usecase/ai reads for every
	// inbound message (buildProductContext) — separate from
	// ListByOrganization (the dashboard's full list, including inactive
	// products a merchant has paused) so that hot path never fetches rows
	// it would just filter back out.
	ListActiveByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.Product, error)
	Update(ctx context.Context, product *entity.Product) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}
