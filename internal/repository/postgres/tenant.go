package postgres

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// withTenant runs fn inside a transaction with app.current_org_id set to
// orgID for that transaction's duration, satisfying the row-level-security
// policies defined in database/schema.sql §17
// (`organization_id = current_setting('app.current_org_id', true)::uuid`).
//
// This MUST be a transaction using SET LOCAL, not a bare SET on the
// connection: SET is connection-scoped and would leak into whatever
// request borrows this connection next from the pool — a real cross-tenant
// data leak, not a hypothetical one. SET LOCAL is scoped to the current
// transaction and is automatically unset on COMMIT/ROLLBACK.
//
// Every repository method that touches an RLS-protected table (see the
// table list in schema.sql §17) must go through this helper. A method that
// doesn't know its org_id up front (e.g. resolving a tenant FROM an
// external id, as InstagramAccountRepository.FindByIGUserID has to during
// webhook ingestion) cannot use this helper and needs its own documented
// exception — see the comment on that method.
func withTenant(ctx context.Context, db *gorm.DB, orgID uuid.UUID, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// set_config(), not a parameterized "SET LOCAL ... = ?": Postgres's
		// SET is a utility statement whose grammar does not accept a bind
		// parameter in the value position — "SET LOCAL app.x = $1" is a
		// syntax error (SQLSTATE 42601), full stop, regardless of the
		// driver. set_config() is a regular function call, so it can be
		// parameterized safely (no string-building, no injection risk from
		// orgID). The third argument (true = is_local) makes this
		// transaction-scoped, the exact equivalent of SET LOCAL — see the
		// doc comment above for why that scoping matters.
		if err := tx.Exec("SELECT set_config('app.current_org_id', ?, true)", orgID.String()).Error; err != nil {
			return err
		}
		return fn(tx)
	})
}
