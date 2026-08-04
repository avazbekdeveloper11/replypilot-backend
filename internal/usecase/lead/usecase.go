// Package lead is the CRUD behind the Leads dashboard page. It does NOT
// capture leads — that happens inside internal/usecase/ai's phone-number
// detection, which writes directly through repository.LeadRepository (see
// that package's Leads port). This usecase only covers what a human does
// afterward: look at the list, mark one contacted/done.
package lead

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type UseCase struct {
	repo repository.LeadRepository
}

func New(repo repository.LeadRepository) *UseCase {
	return &UseCase{repo: repo}
}

func (uc *UseCase) List(ctx context.Context, orgID uuid.UUID, status *entity.LeadStatus) ([]*entity.Lead, error) {
	return uc.repo.ListByOrganization(ctx, orgID, status)
}

func (uc *UseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*entity.Lead, error) {
	return uc.repo.FindByID(ctx, orgID, id)
}

func validStatus(s entity.LeadStatus) bool {
	switch s {
	case entity.LeadStatusNew, entity.LeadStatusContacted, entity.LeadStatusDone:
		return true
	default:
		return false
	}
}

func (uc *UseCase) UpdateStatus(ctx context.Context, orgID, id uuid.UUID, status entity.LeadStatus) (*entity.Lead, error) {
	if !validStatus(status) {
		return nil, apperror.InvalidInput("invalid lead status", nil)
	}
	return uc.repo.UpdateStatus(ctx, orgID, id, status)
}
