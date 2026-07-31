package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type RoleRepository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*entity.Role, error)
	// FindSystemRoleByName looks up one of the four seeded, org_id-NULL
	// roles (Owner/Admin/Agent/Viewer) — used when provisioning the first
	// TeamMember for a newly created organization.
	FindSystemRoleByName(ctx context.Context, name string) (*entity.Role, error)
	// ListSystemRoles returns all four seeded roles — backs the role
	// picker on the Team page's invite form. Custom per-org roles (schema
	// supports them, no usecase does yet) would need a second, org-scoped
	// query; out of scope until that feature exists.
	ListSystemRoles(ctx context.Context) ([]*entity.Role, error)
}
