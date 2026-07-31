// Package platformsettings manages ReplyPilot-staff-configured, platform-
// wide secrets stored in the platform_settings table — currently just the
// Gemini API key (internal/usecase/ai and internal/usecase/knowledgebase
// both call Google's Gemini API using one key that belongs to ReplyPilot,
// not to any tenant). Kept as its own small package, not folded into
// usecase/admin, specifically so it has a minimal dependency footprint
// (just this one repository + the shared encryptor) — both cmd/api and
// cmd/worker-ai need to READ the current key on a timer (see
// internal/platform/geminikey) without pulling in usecase/admin's org/
// stats repositories, which they have no other reason to depend on.
// usecase/admin composes this package for the two HTTP endpoints instead
// of duplicating it — see AdminHandler's second constructor argument.
package platformsettings

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// GeminiAPIKeyKey is the platform_settings row this package manages today.
// Exported so internal/platform/geminikey's poller (constructed
// separately in each cmd/*, without a UseCase instance — see that
// package's doc comment) can call the repository directly with the same
// key string, without this package needing to expose a poller-specific
// method.
const GeminiAPIKeyKey = "gemini_api_key"

type UseCase struct {
	repo      repository.PlatformSettingsRepository
	encryptor *crypto.AESGCMEncryptor
}

func New(repo repository.PlatformSettingsRepository, encryptor *crypto.AESGCMEncryptor) *UseCase {
	return &UseCase{repo: repo, encryptor: encryptor}
}

// GeminiKeyStatus is what the admin panel is allowed to see: whether a key
// is configured and when it was last changed — never the key itself. Once
// set, a secret in this codebase is write-only from the HTTP layer's
// perspective, same principle as InstagramAccount.AccessTokenEncrypted
// never appearing in InstagramAccountResponse.
type GeminiKeyStatus struct {
	Configured bool
	UpdatedAt  *time.Time
}

// Status reports whether a Gemini key is currently configured in the DB
// and when it was last set — used by GET /v1/admin/settings/gemini. Does
// NOT decrypt the value; there's nothing in this struct that needs it.
func (uc *UseCase) Status(ctx context.Context) (GeminiKeyStatus, error) {
	setting, found, err := uc.repo.Get(ctx, GeminiAPIKeyKey)
	if err != nil {
		return GeminiKeyStatus{}, err
	}
	if !found {
		return GeminiKeyStatus{Configured: false}, nil
	}
	updatedAt := setting.UpdatedAt
	return GeminiKeyStatus{Configured: true, UpdatedAt: &updatedAt}, nil
}

// ResolveGeminiAPIKey decrypts and returns the currently configured key.
// Internal use only — this is what internal/platform/geminikey's poller
// calls to find out whether the key changed, NOT something an HTTP
// handler should ever call; there is no GET endpoint that returns this.
// found=false (not an error) means nothing has been configured in the DB
// yet, e.g. a fresh install still running on GEMINI_API_KEY alone.
func (uc *UseCase) ResolveGeminiAPIKey(ctx context.Context) (key string, found bool, err error) {
	setting, found, err := uc.repo.Get(ctx, GeminiAPIKeyKey)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	plaintext, err := uc.encryptor.Decrypt(setting.ValueEncrypted)
	if err != nil {
		return "", false, apperror.Internal("decrypt gemini api key", err)
	}
	return plaintext, true, nil
}

// SetGeminiAPIKey encrypts and upserts the key — used by
// PUT /v1/admin/settings/gemini. Rejects an empty string rather than
// silently storing one: "unset the key" isn't a feature this endpoint
// offers today (there's no product reason to go back to an unconfigured
// state once a real key exists), so an empty value is almost certainly a
// client bug, not intent.
func (uc *UseCase) SetGeminiAPIKey(ctx context.Context, apiKey string, updatedBy uuid.UUID) error {
	if apiKey == "" {
		return apperror.InvalidInput("api_key must not be empty", nil)
	}
	encrypted, err := uc.encryptor.Encrypt(apiKey)
	if err != nil {
		return apperror.Internal("encrypt gemini api key", err)
	}
	return uc.repo.Set(ctx, GeminiAPIKeyKey, encrypted, updatedBy)
}
