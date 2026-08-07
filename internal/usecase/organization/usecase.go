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
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

// businessHoursTimeLayout is "HH:MM" (24-hour), the only format the
// Settings page's business-hours form sends/receives — see
// UpdateBusinessHours and the mirrored formatting in
// v1.toOrgResponse/OrgResponse.
const businessHoursTimeLayout = "15:04"

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

// UpdateBusinessHours enables/disables and sets the daily window during
// which the AI auto-replies — see internal/usecase/ai's
// withinBusinessHours for how this is enforced. start/end are "HH:MM"
// (24-hour) strings; both are required when enabled is true, ignored
// (stored as nil) when enabled is false, so disabling doesn't leave a
// stale window behind that a re-enable would silently resurrect.
func (uc *UseCase) UpdateBusinessHours(ctx context.Context, orgID uuid.UUID, enabled bool, start, end string) (*entity.Organization, error) {
	org, err := uc.orgRepo.FindByID(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var startMinutes, endMinutes *int
	if enabled {
		sm, parseErr := parseClockMinutes(start)
		if parseErr != nil {
			return nil, apperror.InvalidInput("business_hours_start must be HH:MM", parseErr)
		}
		em, parseErr := parseClockMinutes(end)
		if parseErr != nil {
			return nil, apperror.InvalidInput("business_hours_end must be HH:MM", parseErr)
		}
		startMinutes, endMinutes = &sm, &em
	}

	if err := uc.orgRepo.UpdateBusinessHours(ctx, orgID, enabled, startMinutes, endMinutes); err != nil {
		return nil, err
	}

	org.BusinessHoursEnabled = enabled
	org.BusinessHoursStartMinutes = startMinutes
	org.BusinessHoursEndMinutes = endMinutes
	return org, nil
}

func parseClockMinutes(s string) (int, error) {
	t, err := time.Parse(businessHoursTimeLayout, s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}
