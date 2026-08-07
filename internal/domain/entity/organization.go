package entity

import (
	"time"

	"github.com/google/uuid"
)

type OrganizationStatus string

const (
	OrganizationStatusTrial     OrganizationStatus = "trial"
	OrganizationStatusActive    OrganizationStatus = "active"
	OrganizationStatusSuspended OrganizationStatus = "suspended"
	OrganizationStatusCancelled OrganizationStatus = "cancelled"
)

// Organization is a tenant. Every other business entity in the system is
// scoped to exactly one Organization, enforced both here (application-level
// filtering in every repository query) and in Postgres via row-level
// security — see database/schema.sql §17.
type Organization struct {
	ID        uuid.UUID
	Name      string
	Slug      string
	Status    OrganizationStatus
	Timezone  string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	CreatedBy *uuid.UUID
	UpdatedBy *uuid.UUID

	// BusinessHoursEnabled/StartMinutes/EndMinutes gate automated AI
	// replies to a daily window, applied in Timezone above — see
	// usecase/ai's withinBusinessHours. Minutes-since-midnight (0-1439),
	// not a time.Time, since there's no date component; nil start/end
	// means "not configured yet" even if Enabled is somehow true.
	BusinessHoursEnabled      bool
	BusinessHoursStartMinutes *int
	BusinessHoursEndMinutes   *int
}
