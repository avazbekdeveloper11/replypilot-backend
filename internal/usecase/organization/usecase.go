// Package organization is intentionally thin — organization creation lives
// in usecase/auth (Register), since a new org is always created alongside
// its first Owner user, never standalone. This usecase covers reads plus
// the settings update behind the Settings page (name, timezone).
// Suspension, deletion, etc. follow the same pattern and are the obvious
// next additions — not included here to avoid the padding of methods with
// no caller in this codebase.
package organization

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type UseCase struct {
	orgRepo repository.OrganizationRepository
}

func New(orgRepo repository.OrganizationRepository) *UseCase {
	return &UseCase{orgRepo: orgRepo}
}

func (uc *UseCase) GetByID(ctx context.Context, id uuid.UUID) (*entity.Organization, error) {
	return uc.orgRepo.FindByID(ctx, id)
}

// UpdateSettings updates the two fields the Settings page actually
// exposes — name and timezone. Slug is deliberately not editable here: it
// is embedded in the Instagram OAuth redirect URL's org resolution and in
// any links already shared/bookmarked, so changing it needs a dedicated,
// more careful flow (redirects, confirmation) that's out of scope for a
// simple settings form.
func (uc *UseCase) UpdateSettings(ctx context.Context, orgID uuid.UUID, name, timezone string) (*entity.Organization, error) {
	org, err := uc.orgRepo.FindByID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, apperror.InvalidInput("name is required", nil)
	}
	org.Name = name
	if timezone != "" {
		org.Timezone = timezone
	}
	if err := uc.orgRepo.Update(ctx, org); err != nil {
		return nil, err
	}
	return org, nil
}
