package telegram

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/pkg/crypto"
)

// NotifyUseCase pushes admin-facing Telegram notifications through the
// org's already-connected bot (see migration 000021's header comment) — a
// new lead captured, or a payment completed. It is deliberately the only
// thing in this package that reads NotifyChatID/NotifyOnLead/NotifyOnPayment;
// handlePlainMessage (webhook_usecase.go) is the only thing that writes
// NotifyChatID, and GenerateNotifyCode (connect_usecase.go) the only thing
// that writes NotifyVerifyCode.
//
// Both exported methods are fire-and-forget: every failure (no verified
// chat, decrypt error, Telegram API error) is swallowed and logged, never
// returned. This mirrors ai.UseCase.captureLeadIfPresent's and
// payment.WebhookUseCase.notifyPaid's own posture — by the time either
// calls into here, the customer-facing work (the AI's reply, the paid
// order) has already succeeded, so a notification hiccup must never surface
// as a failure of that work.
//
// A single *NotifyUseCase instance satisfies both ai.Notifier and
// payment.Notifier — two narrow, independently-declared interfaces (see
// each package's own Notifier doc comment for why they don't share one
// definition — "usecases don't depend on each other").
type NotifyUseCase struct {
	repo   repository.TelegramAccountRepository
	sender PlainSender
	enc    *crypto.AESGCMEncryptor
	logger *zap.Logger
}

func NewNotifyUseCase(repo repository.TelegramAccountRepository, sender PlainSender, enc *crypto.AESGCMEncryptor, logger *zap.Logger) *NotifyUseCase {
	return &NotifyUseCase{repo: repo, sender: sender, enc: enc, logger: logger}
}

// NotifyLead sends a short DM to the org's verified notify chat, if any,
// when ai.UseCase.captureLeadIfPresent records a new lead. phone and
// summary mirror entity.Lead's own fields exactly, not reformatted.
func (uc *NotifyUseCase) NotifyLead(ctx context.Context, orgID uuid.UUID, phone, summary string) {
	text := fmt.Sprintf("🆕 Yangi lead!\nTelefon: %s", phone)
	if summary != "" {
		text += "\n" + summary
	}
	uc.notify(ctx, orgID, func(a *entity.TelegramAccount) bool { return a.NotifyOnLead }, text)
}

// NotifyPayment mirrors NotifyLead — see payment.WebhookUseCase.notifyPaid,
// its only caller. amountSom is already formatted (whole so'm, no
// decimals) by the caller, same as the customer-facing thank-you message
// notifyPaid sends alongside this.
func (uc *NotifyUseCase) NotifyPayment(ctx context.Context, orgID uuid.UUID, productName, amountSom string) {
	text := fmt.Sprintf("💰 To'lov qabul qilindi!\n%s — %s so'm", productName, amountSom)
	uc.notify(ctx, orgID, func(a *entity.TelegramAccount) bool { return a.NotifyOnPayment }, text)
}

// notify resolves the org's connected bot (ListByOrganization — see
// ConnectUseCase's doc comment on "one bot per organization for now"),
// checks it has a verified NotifyChatID and the relevant toggle enabled,
// then sends. Every early return here is a legitimate "nothing to do" case
// (no bot connected, never verified, this notification kind is off), not
// an error worth logging — only the send/decrypt path logs on failure.
func (uc *NotifyUseCase) notify(ctx context.Context, orgID uuid.UUID, enabled func(*entity.TelegramAccount) bool, text string) {
	accounts, err := uc.repo.ListByOrganization(ctx, orgID)
	if err != nil || len(accounts) == 0 {
		return
	}
	account := accounts[0]
	if account.NotifyChatID == nil || !enabled(account) {
		return
	}

	botToken, err := uc.enc.Decrypt(account.BotTokenEncrypted)
	if err != nil {
		uc.logger.Warn("telegram notify: decrypt bot token failed",
			zap.String("telegram_account_id", account.ID.String()), zap.Error(err))
		return
	}
	if err := uc.sender.SendPlainMessage(ctx, botToken, *account.NotifyChatID, text); err != nil {
		uc.logger.Warn("telegram notify: send failed",
			zap.String("telegram_account_id", account.ID.String()), zap.Error(err))
	}
}
