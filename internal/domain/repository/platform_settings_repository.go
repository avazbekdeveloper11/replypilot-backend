package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PlatformSetting is one row of the platform_settings table — a single
// encrypted value keyed by a well-known string (see
// internal/usecase/platformsettings for the keys currently defined, e.g.
// "gemini_api_key"). ValueEncrypted is ciphertext, same convention as
// entity.InstagramAccount.AccessTokenEncrypted — it must never be logged
// or returned from an HTTP response; only the usecase layer decrypts it,
// and only for the one purpose it was fetched for.
type PlatformSetting struct {
	Key            string
	ValueEncrypted []byte
	UpdatedAt      time.Time
	UpdatedBy      *uuid.UUID
}

// PlatformSettingsRepository is a tiny key/value store for ReplyPilot-
// staff-configured, platform-wide secrets. Unlike every tenant-scoped
// repository in this codebase, this one is never called through
// withTenant — platform_settings has no organization_id and no RLS (see
// migration 000010's doc comment), the same "global, not tenant" shape as
// PlanModel.
type PlatformSettingsRepository interface {
	// Get returns (nil, false, nil) — not an error — when the key has
	// never been set. Callers distinguish "not configured yet" from a
	// real failure this way, matching apperror.NotFound's role elsewhere
	// but without forcing every caller to unwrap an apperror just to
	// check presence.
	Get(ctx context.Context, key string) (*PlatformSetting, bool, error)
	// Set upserts — the first call to set a given key creates the row,
	// every call after that overwrites it (and updated_at/updated_by
	// along with it). There is no separate Create/Update split because
	// there is no meaningful "the key didn't exist yet" error case a
	// caller needs to react to differently.
	Set(ctx context.Context, key string, valueEncrypted []byte, updatedBy uuid.UUID) error
}
