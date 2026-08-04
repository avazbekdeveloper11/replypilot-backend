// Package click is the CRUD behind an organization's connection to Click
// (click.uz) — see entity.ClickIntegration's doc comment for why there's no
// token/secret here, unlike internal/usecase/instagram's OAuth flow.
package click

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
)

type UseCase struct {
	repo repository.ClickIntegrationRepository
}

func New(repo repository.ClickIntegrationRepository) *UseCase {
	return &UseCase{repo: repo}
}

type ConnectInput struct {
	OrganizationID    uuid.UUID
	MerchantID        string
	ServiceID         string
	MerchantUserID    *string
	ConnectedByUserID uuid.UUID
}

func (uc *UseCase) Connect(ctx context.Context, in ConnectInput) (*entity.ClickIntegration, error) {
	merchantID := strings.TrimSpace(in.MerchantID)
	serviceID := strings.TrimSpace(in.ServiceID)
	if merchantID == "" {
		return nil, apperror.InvalidInput("merchant_id is required", nil)
	}
	if serviceID == "" {
		return nil, apperror.InvalidInput("service_id is required", nil)
	}

	var merchantUserID *string
	if in.MerchantUserID != nil {
		trimmed := strings.TrimSpace(*in.MerchantUserID)
		if trimmed != "" {
			merchantUserID = &trimmed
		}
	}

	connectedBy := in.ConnectedByUserID
	integration := &entity.ClickIntegration{
		OrganizationID:    in.OrganizationID,
		MerchantID:        merchantID,
		ServiceID:         serviceID,
		MerchantUserID:    merchantUserID,
		ConnectedByUserID: &connectedBy,
	}
	if err := uc.repo.Upsert(ctx, integration); err != nil {
		return nil, err
	}
	return integration, nil
}

// Get returns (nil, nil) — not a NotFound error — when the org has never
// connected Click. Callers that need to know "is Click connected" (the
// settings card, and internal/usecase/ai's per-message check) both read
// more naturally as a nil check than an error-type switch; FindByOrganization
// itself still returns apperror.NotFound to callers that DO want an error
// (none currently do, but the repository contract stays honest either way).
func (uc *UseCase) Get(ctx context.Context, orgID uuid.UUID) (*entity.ClickIntegration, error) {
	integration, err := uc.repo.FindByOrganization(ctx, orgID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}
	return integration, nil
}

func (uc *UseCase) Disconnect(ctx context.Context, orgID uuid.UUID) error {
	return uc.repo.Delete(ctx, orgID)
}
