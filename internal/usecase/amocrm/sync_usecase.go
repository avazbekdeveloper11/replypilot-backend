package amocrm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// refreshBuffer is how far ahead of AccessTokenExpiresAt SyncUseCase
// proactively refreshes — chosen so a single sync call (a handful of
// HTTP requests) never straddles the token's actual expiry mid-call.
// amoCRM access tokens live ~24h, so this is a small fraction of that,
// not a meaningful reduction in how often refreshes happen.
const refreshBuffer = 5 * time.Minute

// ConversationLookup is the narrow slice of ConversationRepository this
// package needs — declared locally rather than depending on the full
// interface, same "usecases don't depend on each other, narrow ports
// declared per package" convention as e.g. campaign.ProductLister.
type ConversationLookup interface {
	FindByID(ctx context.Context, orgID, conversationID uuid.UUID) (*entity.Conversation, error)
}

// OrderLister is the narrow slice of OrderRepository this package
// needs.
type OrderLister interface {
	ListByConversation(ctx context.Context, orgID, conversationID uuid.UUID) ([]*entity.Order, error)
}

type SyncUseCase struct {
	repo          repository.AmoCRMRepository
	api           APIClient
	encryptor     *crypto.AESGCMEncryptor
	conversations ConversationLookup
	orders        OrderLister
}

func NewSyncUseCase(
	repo repository.AmoCRMRepository,
	api APIClient,
	encryptor *crypto.AESGCMEncryptor,
	conversations ConversationLookup,
	orders OrderLister,
) *SyncUseCase {
	return &SyncUseCase{repo: repo, api: api, encryptor: encryptor, conversations: conversations, orders: orders}
}

// SyncCustomer pushes one customer (conversation) to the org's
// connected amoCRM account as a contact, then adds a note summarizing
// their paid-order history — the same purchase-history data the
// customer database (internal/usecase/customer) shows. Creates a new
// amoCRM contact the first time a conversation is synced; every
// subsequent call updates that same contact (via
// AmoCRMContactLink — see its doc comment) rather than creating
// duplicates.
//
// Returns apperror.NotFound (surfaced by the handler as "connect
// amoCRM first") if the org has never connected amoCRM, and
// apperror.Unauthorized if the connection has expired/been revoked and
// needs reconnecting — both are conditions the caller should show the
// user directly, not swallow.
func (uc *SyncUseCase) SyncCustomer(ctx context.Context, orgID, conversationID uuid.UUID) (*entity.AmoCRMContactLink, error) {
	integration, err := uc.repo.FindByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// FindByID first, same reasoning as customer.UseCase.Orders' identical
	// guard: turns a conversation id belonging to a different org into a
	// clean 404 instead of relying solely on RLS.
	conv, err := uc.conversations.FindByID(ctx, orgID, conversationID)
	if err != nil {
		return nil, err
	}

	orders, err := uc.orders.ListByConversation(ctx, orgID, conversationID)
	if err != nil {
		return nil, err
	}

	accessToken, err := uc.resolveAccessToken(ctx, integration)
	if err != nil {
		return nil, err
	}

	name := customerDisplayName(conv)

	link, err := uc.repo.FindContactLink(ctx, orgID, conversationID)
	if err != nil {
		return nil, err
	}

	var contactID int64
	if link != nil {
		contactID = link.AmoCRMContactID
		if err := uc.api.UpdateContact(ctx, integration.Subdomain, accessToken, contactID, name); err != nil {
			return nil, apperror.Internal("update amocrm contact", err)
		}
	} else {
		contactID, err = uc.api.CreateContact(ctx, integration.Subdomain, accessToken, name)
		if err != nil {
			return nil, apperror.Internal("create amocrm contact", err)
		}
	}

	if note := buildPurchaseSummaryNote(orders); note != "" {
		// Deliberately non-fatal: the contact create/update above is the
		// part of this call that actually matters (the customer now
		// exists in amoCRM, findable and linked), and a transient note-add
		// failure shouldn't make the whole Sync call look like it failed
		// to the caller — that risks a confused retry that creates a
		// second, duplicate contact if the retry logic above ever changes.
		// Silently continuing here is a deliberate scope tradeoff, not an
		// oversight: this package has no logger dependency (unlike e.g.
		// instagram.WebhookUseCase), and adding one for a single
		// best-effort call site isn't worth it yet.
		_ = uc.api.AddNote(ctx, integration.Subdomain, accessToken, contactID, note)
	}

	newLink := &entity.AmoCRMContactLink{OrganizationID: orgID, ConversationID: conversationID, AmoCRMContactID: contactID}
	if err := uc.repo.UpsertContactLink(ctx, newLink); err != nil {
		return nil, err
	}
	return newLink, nil
}

// resolveAccessToken decrypts the stored access token, refreshing it
// first (proactively, not reactively on a 401 — see refreshBuffer's
// doc comment) if it's within refreshBuffer of expiring. On a refresh
// failure — the refresh token itself expired (unused >3 months) or was
// revoked — flips the integration to Expired and returns
// apperror.Unauthorized so the caller knows to prompt a reconnect,
// rather than a generic Internal error.
func (uc *SyncUseCase) resolveAccessToken(ctx context.Context, integration *entity.AmoCRMIntegration) (string, error) {
	if time.Now().Add(refreshBuffer).Before(integration.AccessTokenExpiresAt) {
		return uc.encryptor.Decrypt(integration.AccessTokenEncrypted)
	}

	refreshToken, err := uc.encryptor.Decrypt(integration.RefreshTokenEncrypted)
	if err != nil {
		return "", apperror.Internal("decrypt amocrm refresh token", err)
	}

	accessToken, newRefreshToken, expiresIn, err := uc.api.RefreshToken(ctx, integration.Subdomain, refreshToken)
	if err != nil {
		integration.Status = entity.AmoCRMIntegrationStatusExpired
		_ = uc.repo.Update(ctx, integration) // best-effort; the sync itself still fails below regardless
		return "", apperror.Unauthorized("amoCRM connection expired — please reconnect")
	}

	encryptedAccess, err := uc.encryptor.Encrypt(accessToken)
	if err != nil {
		return "", apperror.Internal("encrypt amocrm access token", err)
	}
	encryptedRefresh, err := uc.encryptor.Encrypt(newRefreshToken)
	if err != nil {
		return "", apperror.Internal("encrypt amocrm refresh token", err)
	}

	integration.AccessTokenEncrypted = encryptedAccess
	integration.RefreshTokenEncrypted = encryptedRefresh
	integration.AccessTokenExpiresAt = time.Now().Add(expiresIn)
	integration.Status = entity.AmoCRMIntegrationStatusConnected
	if err := uc.repo.Update(ctx, integration); err != nil {
		return "", err
	}
	return accessToken, nil
}

// customerDisplayName is what shows up as the amoCRM contact's name.
// Falls back to a channel-qualified placeholder rather than an empty
// string — amoCRM's API rejects a contact with no name.
func customerDisplayName(conv *entity.Conversation) string {
	if conv.CustomerUsername != nil && strings.TrimSpace(*conv.CustomerUsername) != "" {
		return strings.TrimSpace(*conv.CustomerUsername)
	}
	channel := string(conv.Channel)
	if channel != "" {
		// Capitalize manually rather than the deprecated strings.Title —
		// these are always plain-ASCII channel names ("instagram",
		// "telegram"), so no Unicode/locale handling is needed.
		channel = strings.ToUpper(channel[:1]) + channel[1:]
	}
	return fmt.Sprintf("%s customer (%s)", channel, conv.ID.String()[:8])
}

// buildPurchaseSummaryNote renders every PAID order (unpaid/failed
// attempts aren't worth cluttering an amoCRM note with) as a short
// plain-text summary. Returns "" — no note is added — when there are no
// paid orders yet, since "this customer hasn't bought anything" isn't
// worth a note on a freshly-created contact.
func buildPurchaseSummaryNote(orders []*entity.Order) string {
	var lines []string
	var totalCents int64
	currency := "UZS"
	for _, o := range orders {
		if o.Status != entity.OrderStatusPaid {
			continue
		}
		totalCents += o.AmountCents
		currency = o.Currency
		paidAt := ""
		if o.PaidAt != nil {
			paidAt = o.PaidAt.Format("2006-01-02")
		}
		lines = append(lines, fmt.Sprintf("- %s: %s %d (%s)", paidAt, o.Currency, o.AmountCents, o.ProductNameSnapshot))
	}
	if len(lines) == 0 {
		return ""
	}
	header := fmt.Sprintf("ReplyPilot purchase history — %d paid order(s), total %s %d:", len(lines), currency, totalCents)
	return header + "\n" + strings.Join(lines, "\n")
}
