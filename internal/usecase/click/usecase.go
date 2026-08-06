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
	"github.com/replypilot/backend/pkg/crypto"
)

type UseCase struct {
	repo      repository.ClickIntegrationRepository
	encryptor *crypto.AESGCMEncryptor
}

func New(repo repository.ClickIntegrationRepository, encryptor *crypto.AESGCMEncryptor) *UseCase {
	return &UseCase{repo: repo, encryptor: encryptor}
}

type ConnectInput struct {
	OrganizationID uuid.UUID
	MerchantID     string
	ServiceID      string
	MerchantUserID *string
	// SecretKey is Click's own webhook-signing secret (from the org's Click
	// merchant cabinet, distinct from MerchantID/ServiceID — see
	// entity.ClickIntegration.SecretKeyEncrypted's doc comment). Required:
	// without it, payment.WebhookUseCase can never verify a Prepare/Complete
	// callback for this org, so a payment link would be sent but never
	// actually confirmed.
	SecretKey         string
	ConnectedByUserID uuid.UUID
}

func (uc *UseCase) Connect(ctx context.Context, in ConnectInput) (*entity.ClickIntegration, error) {
	merchantID := strings.TrimSpace(in.MerchantID)
	serviceID := strings.TrimSpace(in.ServiceID)
	secretKey := strings.TrimSpace(in.SecretKey)
	if merchantID == "" {
		return nil, apperror.InvalidInput("merchant_id is required", nil)
	}
	if serviceID == "" {
		return nil, apperror.InvalidInput("service_id is required", nil)
	}
	if secretKey == "" {
		return nil, apperror.InvalidInput("secret_key is required", nil)
	}

	var merchantUserID *string
	if in.MerchantUserID != nil {
		trimmed := strings.TrimSpace(*in.MerchantUserID)
		if trimmed != "" {
			merchantUserID = &trimmed
		}
	}

	encryptedSecret, err := uc.encryptor.Encrypt(secretKey)
	if err != nil {
		return nil, apperror.Internal("encrypt click secret key", err)
	}

	connectedBy := in.ConnectedByUserID
	integration := &entity.ClickIntegration{
		OrganizationID:     in.OrganizationID,
		MerchantID:         merchantID,
		ServiceID:          serviceID,
		MerchantUserID:     merchantUserID,
		SecretKeyEncrypted: encryptedSecret,
		ConnectedByUserID:  &connectedBy,
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
