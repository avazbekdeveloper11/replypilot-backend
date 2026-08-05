package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

type TelegramAccountRepository interface {
	Create(ctx context.Context, account *entity.TelegramAccount) error
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TelegramAccount, error)
	// FindByIDForWebhook is the Telegram-side counterpart to
	// InstagramAccountRepository.FindByIGUserID: called from the webhook
	// ingestion path (telegram.WebhookUseCase), before any tenant context
	// exists, using the account id Telegram's webhook URL itself carries
	// (see migration 000014's webhook_account_lookup policy) rather than a
	// secondary lookup field — the URL already tells us which account this
	// is, unlike Meta's webhook payload which only carries the IG business
	// account id and needs FindByIGUserID to resolve it.
	FindByIDForWebhook(ctx context.Context, id uuid.UUID) (*entity.TelegramAccount, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]*entity.TelegramAccount, error)
	Update(ctx context.Context, account *entity.TelegramAccount) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}
