// Package repository defines the ports (interfaces) usecases depend on.
// Concrete adapters live in internal/repository/postgres — usecases import
// only this package, never gorm or database/sql directly. That's the whole
// point of the Repository Pattern here: swap Postgres for something else,
// or mock it in tests, without touching business logic.
package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type OrganizationRepository interface {
	Create(ctx context.Context, org *entity.Organization) error
	FindByID(ctx context.Context, id uuid.UUID) (*entity.Organization, error)
	FindBySlug(ctx context.Context, slug string) (*entity.Organization, error)
	Update(ctx context.Context, org *entity.Organization) error
	// UpdateBusinessHours writes just the business-hours gating fields —
	// see the postgres implementation's doc comment on why this isn't
	// folded into Update.
	UpdateBusinessHours(ctx context.Context, orgID uuid.UUID, enabled bool, startMinutes, endMinutes *int) error
}
