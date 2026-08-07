// Package amocrm is ReplyPilot's integration with amoCRM, the
// most-used CRM in this market: an org connects its own amoCRM account
// via OAuth 2.0 (this file), then can push individual customers from
// the customer database as amoCRM contacts, with a note summarizing
// their purchase history (sync_usecase.go).
//
// Scope, honestly: this is a one-way (ReplyPilot -> amoCRM), on-demand
// sync — triggered per-customer from the dashboard, not automatic on
// every new order or message. It does not create amoCRM leads/deals,
// does not read anything back from amoCRM, and does not fire in real
// time off domain events. Each of those is a reasonable next step, not
// included here, in favor of shipping a correct and complete v1.
package amocrm

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// APIClient is the port to amoCRM's REST + OAuth API. The concrete
// adapter (internal/integration/amocrmapi) makes the actual HTTP
// calls — this usecase has zero net/http dependency, which is what
// makes it testable with a fake client.
type APIClient interface {
	ExchangeCode(ctx context.Context, subdomain, code string) (accessToken, refreshToken string, expiresIn time.Duration, err error)
	RefreshToken(ctx context.Context, subdomain, refreshToken string) (accessToken, newRefreshToken string, expiresIn time.Duration, err error)
	CreateContact(ctx context.Context, subdomain, accessToken, name string) (contactID int64, err error)
	UpdateContact(ctx context.Context, subdomain, accessToken string, contactID int64, name string) error
	AddNote(ctx context.Context, subdomain, accessToken string, contactID int64, text string) error
}

// StateStore persists the OAuth CSRF `state` between connect and
// callback — the exact same interface shape as
// internal/usecase/instagram.StateStore, satisfied by the same
// concrete internal/repository/redis.OAuthStateStore instance passed
// in from internal/di (its "oauth:ig:state:" key prefix predates
// amoCRM and is misleading now that it's shared across both
// integrations, but state values are random UUIDs, so there is no
// collision risk between them — not worth a second Redis-backed store
// type just for this).
type StateStore interface {
	Save(ctx context.Context, state string, orgID uuid.UUID) error
	Consume(ctx context.Context, state string) (orgID uuid.UUID, ok bool, err error)
}

type OAuthUseCase struct {
	repo      repository.AmoCRMRepository
	api       APIClient
	states    StateStore
	encryptor *crypto.AESGCMEncryptor
	clientID  string
}

func NewOAuthUseCase(
	repo repository.AmoCRMRepository,
	api APIClient,
	states StateStore,
	encryptor *crypto.AESGCMEncryptor,
	clientID string,
) *OAuthUseCase {
	return &OAuthUseCase{repo: repo, api: api, states: states, encryptor: encryptor, clientID: clientID}
}

// StartConnect generates a single-use CSRF state bound to orgID, stores
// it, and returns amoCRM's authorization URL to redirect the user to —
// same shape as instagram.OAuthUseCase.StartConnect. Returns
// apperror.InvalidInput if the platform-level amoCRM app credentials
// (config.AmoCRMConfig) were never set — amoCRM is an optional
// integration (see that config's doc comment), so a deployment that
// never configured it should get a clear "not available" error here,
// not a broken redirect to an authorization URL missing its client_id.
func (uc *OAuthUseCase) StartConnect(ctx context.Context, orgID uuid.UUID) (authURL, state string, err error) {
	if uc.clientID == "" {
		return "", "", apperror.InvalidInput("amoCRM integration is not configured on this platform", nil)
	}
	state = uuid.NewString()
	if err := uc.states.Save(ctx, state, orgID); err != nil {
		return "", "", apperror.Internal("store oauth state", err)
	}
	return uc.buildAuthorizationURL(state), state, nil
}

// buildAuthorizationURL points at amoCRM's global login/consent page
// (not a per-subdomain URL — the user picks or logs into their account
// there) — see
// https://developers.kommo.com/docs/oauth-20#getting-an-authorization-code.
// mode=post_message is Kommo/amoCRM's popup-friendly redirect mode; the
// frontend callback page (src/features/amocrm) is opened in a popup, not
// a full top-level navigation, so the dashboard tab never unloads.
func (uc *OAuthUseCase) buildAuthorizationURL(state string) string {
	return fmt.Sprintf(
		"https://www.amocrm.ru/oauth?client_id=%s&state=%s&mode=post_message",
		url.QueryEscape(uc.clientID), url.QueryEscape(state),
	)
}

// Complete verifies the CSRF state, then exchanges the authorization
// code for a token pair and stores the connection. The org is taken
// from the STORED state, not the caller — same CSRF reasoning as
// instagram.OAuthUseCase.Complete. referer is amoCRM's own
// "{subdomain}.amocrm.ru" GET param from the callback redirect — it is
// the ONLY place the subdomain is available; the user is never asked
// for it up front. ConnectedByUserID is always left nil here (stored as
// NULL) — this public callback has no authenticated caller to attribute
// the action to, unlike instagram.OAuthUseCase.connect's
// connectedByUserID parameter, which this package doesn't have a
// direct equivalent of yet.
func (uc *OAuthUseCase) Complete(ctx context.Context, state, code, referer string) (*entity.AmoCRMIntegration, error) {
	if state == "" {
		return nil, apperror.InvalidInput("missing oauth state", nil)
	}
	orgID, ok, err := uc.states.Consume(ctx, state)
	if err != nil {
		return nil, apperror.Internal("verify oauth state", err)
	}
	if !ok {
		return nil, apperror.Unauthorized("invalid, expired, or already-used oauth state")
	}

	subdomain, err := subdomainFromReferer(referer)
	if err != nil {
		return nil, apperror.InvalidInput("could not resolve amoCRM subdomain from callback", err)
	}

	accessToken, refreshToken, expiresIn, err := uc.api.ExchangeCode(ctx, subdomain, code)
	if err != nil {
		return nil, apperror.Internal("exchange authorization code", err)
	}

	encryptedAccess, err := uc.encryptor.Encrypt(accessToken)
	if err != nil {
		return nil, apperror.Internal("encrypt access token", err)
	}
	encryptedRefresh, err := uc.encryptor.Encrypt(refreshToken)
	if err != nil {
		return nil, apperror.Internal("encrypt refresh token", err)
	}

	integration := &entity.AmoCRMIntegration{
		OrganizationID:         orgID,
		Subdomain:              subdomain,
		AccessTokenEncrypted:   encryptedAccess,
		RefreshTokenEncrypted:  encryptedRefresh,
		AccessTokenExpiresAt:   time.Now().Add(expiresIn),
		Status:                 entity.AmoCRMIntegrationStatusConnected,
	}
	if err := uc.repo.Upsert(ctx, integration); err != nil {
		return nil, err
	}
	return integration, nil
}

// subdomainRe is a bare DNS label — letters, digits, hyphens, 1-63
// chars, matching amoCRM's own subdomain rules. Anything that doesn't
// match this exactly is rejected outright.
var subdomainRe = regexp.MustCompile(`^[a-zA-Z0-9-]{1,63}$`)

// subdomainFromReferer extracts "example" from "example.amocrm.ru" (or
// the kommo.com equivalent, in case an org's account was migrated to
// that domain — see the package doc comment). amoCRM always sends this
// as a bare hostname with no scheme, per
// https://developers.kommo.com/docs/oauth-20#getting-an-authorization-code,
// but this strips a scheme defensively in case that ever changes.
//
// SECURITY: Callback is a PUBLIC, unauthenticated route (see router.go)
// — `referer` is fully attacker-controlled input. The extracted
// subdomain is later interpolated directly into a URL
// (amocrmapi.baseURL) that this server then POSTs the PLATFORM'S OWN
// amoCRM client_secret to (see Client.exchangeToken). Without a strict
// allowlist check on the extracted value, a referer like
// "attacker.com/evil.amocrm.ru" would previously pass the old
// suffix-only check and produce the subdomain "attacker.com/evil" —
// which resolves to host "attacker.com" once interpolated into a URL,
// exfiltrating the shared platform OAuth secret to an attacker-chosen
// server on every callback. The regexp below rejects anything that
// isn't a bare DNS label (no slashes, no "@", no scheme) before it is
// ever used to build a URL.
func subdomainFromReferer(referer string) (string, error) {
	referer = strings.TrimPrefix(referer, "https://")
	referer = strings.TrimPrefix(referer, "http://")
	for _, suffix := range []string{".amocrm.ru", ".kommo.com"} {
		if strings.HasSuffix(referer, suffix) {
			subdomain := strings.TrimSuffix(referer, suffix)
			if !subdomainRe.MatchString(subdomain) {
				return "", fmt.Errorf("invalid subdomain in referer %q", referer)
			}
			return subdomain, nil
		}
	}
	return "", fmt.Errorf("unrecognized referer domain %q", referer)
}

// Get returns (nil, nil) — not a NotFound error — when the org has
// never connected amoCRM, same convention as click.UseCase.Get.
func (uc *OAuthUseCase) Get(ctx context.Context, orgID uuid.UUID) (*entity.AmoCRMIntegration, error) {
	integration, err := uc.repo.FindByOrganization(ctx, orgID)
	if err != nil {
		if ae, ok := apperror.As(err); ok && ae.Code == apperror.CodeNotFound {
			return nil, nil
		}
		return nil, err
	}
	return integration, nil
}

func (uc *OAuthUseCase) Disconnect(ctx context.Context, orgID uuid.UUID) error {
	return uc.repo.Delete(ctx, orgID)
}
