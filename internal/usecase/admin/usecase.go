// Package admin is ReplyPilot-staff-facing, not tenant-facing — every
// method here is cross-tenant by design and MUST only ever be reachable
// through the RequirePlatformAdmin middleware (internal/delivery/http/
// middleware/auth.go). This is deliberately a separate usecase package
// from usecase/organization (which is tenant-scoped, "my own org's
// settings") rather than a couple of extra methods bolted onto it — the
// authorization model is fundamentally different (any org member can
// eventually reach usecase/organization's methods for their own org;
// nobody but platform staff should ever reach these, for any org).
package admin

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type UseCase struct {
	adminRepo repository.AdminRepository
	orgRepo   repository.OrganizationRepository
}

func New(adminRepo repository.AdminRepository, orgRepo repository.OrganizationRepository) *UseCase {
	return &UseCase{adminRepo: adminRepo, orgRepo: orgRepo}
}

func (uc *UseCase) ListOrganizations(ctx context.Context) ([]repository.OrganizationSummary, error) {
	return uc.adminRepo.ListOrganizations(ctx)
}

func (uc *UseCase) Stats(ctx context.Context) (*repository.PlatformStats, error) {
	return uc.adminRepo.Stats(ctx)
}

// SuspendOrganization sets an org's status to 'suspended', which
// usecase/auth.UseCase.Login and .Refresh now check and reject on — see
// those methods' doc comments. This is a local access gate only: it does
// NOT touch the org's Stripe subscription (no cancel/pause call), so
// billing continues independently unless an operator also handles that
// in Stripe directly. Scope note: takes effect at next login/refresh, not
// mid-session — an already-issued access token keeps working until it
// expires (the same "coarse, not per-request" tradeoff every JWT-based
// auth in this codebase already makes, see middleware.Auth's doc
// comment).
func (uc *UseCase) SuspendOrganization(ctx context.Context, orgID uuid.UUID) (*entity.Organization, error) {
	return uc.setStatus(ctx, orgID, entity.OrganizationStatusSuspended)
}

// ReactivateOrganization sets status back to 'active'. Same scope note as
// SuspendOrganization: local access gate only, no Stripe interaction.
func (uc *UseCase) ReactivateOrganization(ctx context.Context, orgID uuid.UUID) (*entity.Organization, error) {
	return uc.setStatus(ctx, orgID, entity.OrganizationStatusActive)
}

func (uc *UseCase) setStatus(ctx context.Context, orgID uuid.UUID, status entity.OrganizationStatus) (*entity.Organization, error) {
	org, err := uc.orgRepo.FindByID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if org.Status == status {
		return org, nil
	}
	org.Status = status
	if err := uc.orgRepo.Update(ctx, org); err != nil {
		return nil, apperror.Internal("update organization status", err)
	}
	return org, nil
}
