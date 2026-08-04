package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type LeadRepository interface {
	Create(ctx context.Context, lead *entity.Lead) error
	// HasOpen reports whether this conversation already has a
	// status=new lead — see entity.Lead's doc comment on why this
	// guards against creating a duplicate on every subsequent message.
	HasOpen(ctx context.Context, orgID, conversationID uuid.UUID) (bool, error)
	// ListByOrganization: status == nil means "every status", not
	// "status IS NULL" — same nil-means-unfiltered convention as
	// ConversationListParams.Status.
	ListByOrganization(ctx context.Context, orgID uuid.UUID, status *entity.LeadStatus) ([]*entity.Lead, error)
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.Lead, error)
	UpdateStatus(ctx context.Context, orgID, id uuid.UUID, status entity.LeadStatus) (*entity.Lead, error)
}
