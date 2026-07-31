package entity

import (
	"time"

	"github.com/google/uuid"
)

type TeamMemberStatus string

const (
	TeamMemberStatusInvited   TeamMemberStatus = "invited"
	TeamMemberStatusActive    TeamMemberStatus = "active"
	TeamMemberStatusSuspended TeamMemberStatus = "suspended"
	TeamMemberStatusRemoved   TeamMemberStatus = "removed"
)

// TeamMember joins a User to an Organization with a Role. This is the
// membership/RBAC edge — a User with no TeamMember row for an org has no
// access to it, regardless of what other orgs they belong to.
type TeamMember struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	UserID         uuid.UUID
	RoleID         uuid.UUID
	Status         TeamMemberStatus
	InvitedBy      *uuid.UUID
	InvitedAt      time.Time
	JoinedAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}
