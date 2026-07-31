// Package instagram implements the two Instagram-facing flows: OAuth
// connect (this file) and webhook ingestion (webhook_usecase.go). They
// share the InstagramAccountRepository but are otherwise independent.
package instagram

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// GraphAPIClient is the port to Meta's Instagram Graph API. The concrete
// adapter (internal/integration/metaapi) makes the actual HTTP calls —
// this usecase has zero net/http dependency, which is what makes it
// testable with a fake client.
type GraphAPIClient interface {
	// ExchangeCodeForShortLivedToken trades an OAuth authorization code for
	// a short-lived (~1 hour) access token, per
	// https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/business-login
	ExchangeCodeForShortLivedToken(ctx context.Context, code string) (accessToken, igUserID string, err error)
	// ExchangeForLongLivedToken upgrades a short-lived token to a
	// long-lived one (~60 days, refreshable — see
	// pkg not — internal/integration/metaapi doc comment for the refresh
	// endpoint this pairs with).
	ExchangeForLongLivedToken(ctx context.Context, shortLivedToken string) (accessToken string, expiresIn time.Duration, err error)
	FetchProfile(ctx context.Context, accessToken, igUserID string) (username string, err error)
	// SubscribeApp subscribes the account to webhook fields — without it no
	// DMs are ever delivered to the webhook receiver. See the method's doc
	// comment in internal/integration/metaapi.
	SubscribeApp(ctx context.Context, accessToken, igUserID, fields string) error
}

// webhookFields is the set of Instagram webhook fields ReplyPilot needs for
// the DM pipeline. Kept minimal deliberately — subscribing to fields you
// don't consume just adds noise to the webhook receiver.
const webhookFields = "messages,messaging_seen,messaging_reactions,messaging_postbacks"

// StateStore persists the OAuth CSRF `state` between connect and callback.
// Implemented by internal/repository/redis.OAuthStateStore.
type StateStore interface {
	Save(ctx context.Context, state string, orgID uuid.UUID) error
	Consume(ctx context.Context, state string) (orgID uuid.UUID, ok bool, err error)
}

type OAuthUseCase struct {
	accountRepo repository.InstagramAccountRepository
	graph       GraphAPIClient
	states      StateStore
	encryptor   *crypto.AESGCMEncryptor
	appID       string
	redirectURL string
}

func NewOAuthUseCase(
	accountRepo repository.InstagramAccountRepository,
	graph GraphAPIClient,
	states StateStore,
	encryptor *crypto.AESGCMEncryptor,
	appID string,
	redirectURL string,
) *OAuthUseCase {
	return &OAuthUseCase{
		accountRepo: accountRepo,
		graph:       graph,
		states:      states,
		encryptor:   encryptor,
		appID:       appID,
		redirectURL: redirectURL,
	}
}

// StartConnect generates a single-use CSRF state bound to orgID, stores it,
// and returns the Meta authorization URL to redirect the user to. Owning
// state generation here (not in the HTTP handler) is what lets Complete
// verify it server-side — the handler never sees an unverified state.
func (uc *OAuthUseCase) StartConnect(ctx context.Context, orgID uuid.UUID) (authURL, state string, err error) {
	state = uuid.NewString()
	if err := uc.states.Save(ctx, state, orgID); err != nil {
		return "", "", apperror.Internal("store oauth state", err)
	}
	return uc.buildAuthorizationURL(state), state, nil
}

func (uc *OAuthUseCase) buildAuthorizationURL(state string) string {
	return fmt.Sprintf(
		"https://www.instagram.com/oauth/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=instagram_business_basic,instagram_business_manage_messages&state=%s",
		uc.appID, uc.redirectURL, state,
	)
}

// Complete verifies the CSRF state, then runs the token exchange +
// subscription. The org is taken from the STORED state, not from the
// caller — this both enforces CSRF and guarantees the account is linked to
// the org that actually started the flow, even if the callback arrives on a
// differently-authenticated request. Returns the resolved org so the caller
// can confirm/authorize.
func (uc *OAuthUseCase) Complete(ctx context.Context, state, code string) (*entity.InstagramAccount, error) {
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
	return uc.connect(ctx, orgID, uuid.Nil, code)
}

// connect exchanges the authorization code for a long-lived token, resolves
// the username, encrypts the token, subscribes the account to webhooks, and
// upserts the InstagramAccount for orgID. Reconnecting an already-linked
// account (e.g. after a revoked token) updates the existing row rather than
// erroring. Unexported — the only entry point is Complete, which verifies
// CSRF state first. connectedByUserID is uuid.Nil when the callback can't
// attribute the action to a specific user (stored as NULL).
func (uc *OAuthUseCase) connect(ctx context.Context, orgID, connectedByUserID uuid.UUID, code string) (*entity.InstagramAccount, error) {
	shortLived, igUserID, err := uc.graph.ExchangeCodeForShortLivedToken(ctx, code)
	if err != nil {
		return nil, apperror.Internal("exchange authorization code", err)
	}

	longLived, expiresIn, err := uc.graph.ExchangeForLongLivedToken(ctx, shortLived)
	if err != nil {
		return nil, apperror.Internal("exchange for long-lived token", err)
	}

	username, err := uc.graph.FetchProfile(ctx, longLived, igUserID)
	if err != nil {
		return nil, apperror.Internal("fetch instagram profile", err)
	}

	encryptedToken, err := uc.encryptor.Encrypt(longLived)
	if err != nil {
		return nil, apperror.Internal("encrypt access token", err)
	}

	// Subscribe the account to webhook fields. Do this BEFORE persisting so
	// the stored webhook_subscribed flag reflects reality. A subscribe
	// failure is not fatal to persistence — we still save the account (the
	// hard-won token must not be lost) with webhook_subscribed=false, then
	// surface the error so the caller knows the account is connected but not
	// yet receiving DMs and can retry.
	subscribeErr := uc.graph.SubscribeApp(ctx, longLived, igUserID, webhookFields)

	expiresAt := time.Now().Add(expiresIn)

	account, err := uc.upsertAccount(ctx, upsertParams{
		orgID:             orgID,
		igUserID:          igUserID,
		username:          username,
		encryptedToken:    encryptedToken,
		expiresAt:         expiresAt,
		connectedByUserID: connectedByUserID,
		webhookSubscribed: subscribeErr == nil,
	})
	if err != nil {
		return nil, err
	}

	if subscribeErr != nil {
		return nil, apperror.Internal("account connected but webhook subscription failed — retry connect", subscribeErr)
	}
	return account, nil
}

type upsertParams struct {
	orgID             uuid.UUID
	igUserID          string
	username          string
	encryptedToken    []byte
	expiresAt         time.Time
	connectedByUserID uuid.UUID
	webhookSubscribed bool
}

// upsertAccount creates a new InstagramAccount or updates the existing one
// for the same ig_user_id (reconnect after a revoked/expired token). The
// create-vs-update branch is the only reason this is extracted — it keeps
// connect readable.
func (uc *OAuthUseCase) upsertAccount(ctx context.Context, p upsertParams) (*entity.InstagramAccount, error) {
	// connected_by_user_id is a nullable FK. A zero UUID would violate it
	// (no such user), so map uuid.Nil to a nil pointer → stored as NULL.
	var connectedBy *uuid.UUID
	if p.connectedByUserID != uuid.Nil {
		connectedBy = &p.connectedByUserID
	}

	existing, err := uc.accountRepo.FindByIGUserID(ctx, p.igUserID)
	if err == nil {
		existing.OrganizationID = p.orgID
		existing.Username = &p.username
		existing.AccessTokenEncrypted = p.encryptedToken
		existing.TokenExpiresAt = &p.expiresAt
		existing.Status = entity.InstagramAccountStatusConnected
		existing.WebhookSubscribed = p.webhookSubscribed
		if connectedBy != nil {
			existing.ConnectedByUserID = connectedBy
		}
		if err := uc.accountRepo.Update(ctx, existing); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
		return nil, err
	}

	account := &entity.InstagramAccount{
		OrganizationID:       p.orgID,
		IGUserID:             p.igUserID,
		Username:             &p.username,
		AccessTokenEncrypted: p.encryptedToken,
		TokenExpiresAt:       &p.expiresAt,
		Status:               entity.InstagramAccountStatusConnected,
		WebhookSubscribed:    p.webhookSubscribed,
		ConnectedByUserID:    connectedBy,
	}
	if err := uc.accountRepo.Create(ctx, account); err != nil {
		return nil, err
	}
	return account, nil
}

func (uc *OAuthUseCase) ListForOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.InstagramAccount, error) {
	return uc.accountRepo.ListByOrganization(ctx, orgID)
}

// Disconnect removes ReplyPilot's own stored access to an Instagram
// account. It does NOT call any Meta API to revoke the token server-side
// — Meta's Instagram Business Login has no clean "revoke this specific
// token" endpoint for an app to call on a user's behalf; the standing
// way a user fully cuts an app's access is from their own Instagram
// "Apps and websites" settings. This method's honest scope is "stop
// ReplyPilot from using this account" (delete the stored, encrypted
// token + stop routing DMs to it), not "revoke Instagram's grant" — the
// frontend confirmation dialog says this explicitly, see
// DisconnectAccountDialog's doc comment.
func (uc *OAuthUseCase) Disconnect(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.accountRepo.Delete(ctx, orgID, id)
}
