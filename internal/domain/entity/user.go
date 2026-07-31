package entity

import (
	"time"

	"github.com/google/uuid"
)

type UserStatus string

const (
	UserStatusActive      UserStatus = "active"
	UserStatusInvited     UserStatus = "invited"
	UserStatusSuspended   UserStatus = "suspended"
	UserStatusDeactivated UserStatus = "deactivated"
)

// User is a global identity, not tenant-scoped — one email logs in once and
// joins organizations through TeamMember, not through a separate row per
// org. PasswordHash is nil for OAuth-only users (e.g. future "Sign in with
// Google").
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash *string
	FullName     string
	AvatarURL    *string
	Status       UserStatus
	// IsPlatformAdmin marks a ReplyPilot staff account with cross-tenant
	// read access (the admin panel) — unrelated to any per-organization
	// role. There is no self-serve way to set this; it's a manual
	// operator action (see backend/README.md's "Platform admin" section),
	// deliberately not exposed through any API.
	IsPlatformAdmin bool
	LastLoginAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
}
