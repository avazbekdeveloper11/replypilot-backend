// Package telegram is the Telegram-channel counterpart to
// internal/usecase/instagram: connecting a bot (this file) and ingesting
// its inbound messages (webhook_usecase.go). See migration 000014's header
// comment for how Telegram Business Bots work end to end, and
// entity.TelegramAccount's doc comment for what gets stored.
//
// Unlike Instagram there is no OAuth handshake here — connecting is: the
// org creates a bot via @BotFather, pastes the token into Settings, this
// usecase validates it (GetMe) and registers our webhook URL for it
// (SetWebhook). Pairing that bot to the org's own Telegram Business account
// is a separate, manual step the org does inside their own Telegram app
// (see the "Add a bot to reply to messages" screen); this codebase only
// finds out that pairing succeeded when Telegram delivers a
// business_connection update — see WebhookUseCase.handleBusinessConnection.
package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// BotClient is the narrow port onto Telegram's Bot API this usecase needs
// for connecting a bot — satisfied by internal/integration/telegramapi.Client.
type BotClient interface {
	GetMe(ctx context.Context, botToken string) (username string, err error)
	SetWebhook(ctx context.Context, botToken, webhookURL, secretToken string) error
}

type ConnectUseCase struct {
	repo            repository.TelegramAccountRepository
	bot             BotClient
	encryptor       *crypto.AESGCMEncryptor
	webhookBaseURL  string
	webhookSecret   string
}

func NewConnectUseCase(
	repo repository.TelegramAccountRepository,
	bot BotClient,
	encryptor *crypto.AESGCMEncryptor,
	webhookBaseURL string,
	webhookSecret string,
) *ConnectUseCase {
	return &ConnectUseCase{
		repo:           repo,
		bot:            bot,
		encryptor:      encryptor,
		webhookBaseURL: webhookBaseURL,
		webhookSecret:  webhookSecret,
	}
}

type ConnectInput struct {
	OrganizationID    uuid.UUID
	BotToken          string
	ConnectedByUserID uuid.UUID
}

// Connect validates botToken against Telegram (GetMe — fails fast on a
// pasted-wrong-thing token instead of saving garbage), stores it encrypted,
// and registers this codebase's webhook URL for it. One bot per
// organization for now (mirrors click.UseCase.Connect's upsert-in-place
// pattern, not a list of bots) — reconnecting with a new token replaces the
// existing row and resets BusinessConnectionID to nil, since a different
// bot has no relationship to whatever business account the old one was
// paired to; the org must redo the in-Telegram pairing step for the new
// bot.
func (uc *ConnectUseCase) Connect(ctx context.Context, in ConnectInput) (*entity.TelegramAccount, error) {
	token := strings.TrimSpace(in.BotToken)
	if token == "" {
		return nil, apperror.InvalidInput("bot_token is required", nil)
	}

	username, err := uc.bot.GetMe(ctx, token)
	if err != nil {
		return nil, apperror.InvalidInput("could not validate bot token with Telegram", err)
	}

	encrypted, err := uc.encryptor.Encrypt(token)
	if err != nil {
		return nil, apperror.Internal("encrypt telegram bot token", err)
	}

	existing, findErr := uc.findExistingForOrg(ctx, in.OrganizationID)
	connectedBy := in.ConnectedByUserID

	var account *entity.TelegramAccount
	if findErr == nil {
		existing.BotTokenEncrypted = encrypted
		existing.BotUsername = &username
		existing.BusinessConnectionID = nil // see doc comment: a new token means a new bot
		existing.Status = entity.TelegramAccountStatusConnected
		existing.ConnectedByUserID = &connectedBy
		if err := uc.repo.Update(ctx, existing); err != nil {
			return nil, err
		}
		account = existing
	} else {
		account = &entity.TelegramAccount{
			OrganizationID:     in.OrganizationID,
			BotTokenEncrypted:  encrypted,
			BotUsername:        &username,
			Status:             entity.TelegramAccountStatusConnected,
			ConnectedByUserID:  &connectedBy,
		}
		if err := uc.repo.Create(ctx, account); err != nil {
			return nil, err
		}
	}

	webhookURL := fmt.Sprintf("%s/webhooks/telegram/%s", strings.TrimRight(uc.webhookBaseURL, "/"), account.ID.String())
	if err := uc.bot.SetWebhook(ctx, token, webhookURL, uc.webhookSecret); err != nil {
		// The account row is already saved (a retry can just call Connect
		// again with the same token) — flag it as erroring rather than
		// leaving Status=connected when Telegram isn't actually going to
		// deliver anything to it.
		account.Status = entity.TelegramAccountStatusError
		_ = uc.repo.Update(ctx, account)
		return nil, apperror.Internal("register telegram webhook", err)
	}

	return account, nil
}

func (uc *ConnectUseCase) findExistingForOrg(ctx context.Context, orgID uuid.UUID) (*entity.TelegramAccount, error) {
	accounts, err := uc.repo.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, apperror.NotFound("no telegram account for organization")
	}
	return accounts[0], nil
}

func (uc *ConnectUseCase) ListForOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.TelegramAccount, error) {
	return uc.repo.ListByOrganization(ctx, orgID)
}

// Disconnect deletes the stored bot connection. It does not call
// Telegram's deleteWebhook or otherwise unregister anything bot-side —
// same honest scope as instagram.OAuthUseCase.Disconnect: this stops
// ReplyPilot from using the bot, it does not revoke or reconfigure
// anything on Telegram's side. A dangling webhook pointed at a deleted
// account id just 404s on delivery and Telegram backs off retrying, same
// as it would for any dead endpoint.
func (uc *ConnectUseCase) Disconnect(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.repo.Delete(ctx, orgID, id)
}
