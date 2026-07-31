package entity

import (
	"time"

	"github.com/google/uuid"
)

// Well-known system role names seeded by database/schema.sql §18. System
// roles have OrganizationID == nil and are shared, read-only, visible to
// every tenant. A tenant may additionally define custom roles scoped to
// itself (OrganizationID != nil) — not implemented by any usecase in this
// skeleton yet, but the schema and RoleRepository already support it.
const (
	SystemRoleOwner  = "Owner"
	SystemRoleAdmin  = "Admin"
	SystemRoleAgent  = "Agent"
	SystemRoleViewer = "Viewer"
)

type Role struct {
	ID             uuid.UUID
	OrganizationID *uuid.UUID
	Name           string
	Description    *string
	IsSystem       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}
