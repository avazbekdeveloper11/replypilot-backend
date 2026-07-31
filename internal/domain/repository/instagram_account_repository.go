package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type InstagramAccountRepository interface {
	Create(ctx context.Context, account *entity.InstagramAccount) error
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.InstagramAccount, error)
	FindByIGUserID(ctx context.Context, igUserID string) (*entity.InstagramAccount, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.InstagramAccount, error)
	// ListNearingExpiry returns every connected account (across ALL
	// organizations — see the method's postgres doc comment for why this
	// is cross-tenant by design) whose TokenExpiresAt falls within the
	// next `within` duration. Built for cmd/token-refresh; nothing else
	// should call this.
	ListNearingExpiry(ctx context.Context, within time.Duration) ([]*entity.InstagramAccount, error)
	Update(ctx context.Context, account *entity.InstagramAccount) error
	// Delete soft-deletes the row (see InstagramAccountModel's DeletedAt) —
	// this is the "disconnect" action. It does not revoke the token at
	// Meta's end; see usecase/instagram.OAuthUseCase.Disconnect's doc
	// comment for why that's out of this codebase's reach.
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}
