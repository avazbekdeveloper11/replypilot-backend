package postgres

import (
	"context"

	"gorm.io/gorm"
)

// withPlatformAdmin runs fn inside a transaction with the session GUC
// app.platform_admin set to 'on' for that transaction's duration — the
// elevated-read counterpart to withTenant, satisfying the
// platform_admin_read policies added by migration 000008 on the
// RLS-protected tables the admin panel needs to aggregate across every
// tenant (team_members, conversations, messages, subscriptions).
//
// Same SET LOCAL reasoning as withTenant and the webhook_lookup GUC (see
// those doc comments): transaction-scoped, not connection-scoped, so the
// elevated read cannot leak to any other query sharing the pooled
// connection once this transaction commits or rolls back. Unlike
// webhook_lookup's `SET LOCAL app.webhook_lookup = 'on'` (a fixed
// literal — no bind parameter needed since there's no per-call value),
// this is likewise a fixed literal, not set_config() with a parameter.
//
// AdminRepository is the ONLY caller of this helper — every other
// repository continues to scope its own tenant's data through withTenant.
// Callers of AdminRepository (internal/usecase/admin) MUST be gated by
// the RequirePlatformAdmin middleware; this helper itself performs no
// authorization check, it only sets the GUC that Postgres's RLS policies
// key off.
func withPlatformAdmin(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL app.platform_admin = 'on'").Error; err != nil {
			return err
		}
		return fn(tx)
	})
}
