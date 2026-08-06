// Package payment turns a Click (click.uz) payment link (built by
// internal/usecase/ai.buildProductContext, sent by the AI inside a DM) into
// a confirmed sale: verifying Click's Prepare/Complete Shop API webhook
// (internal/integration/clickapi), tracking the resulting entity.Order, and
// — once a payment actually completes — telling both sides what happened:
// the customer gets a thank-you message over the same channel they paid
// through, and the org's dashboard gets a system message in that same
// conversation naming exactly which product/service was paid for, so
// whoever's watching the inbox knows what to fulfil. See WebhookUseCase.notifyPaid.
package payment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/domain/repository"
	"github.com/replypilot/backend/internal/integration/clickapi"
	"github.com/replypilot/backend/pkg/crypto"
)

// ClickIntegrationLookup is the narrow port onto resolving which org a
// Click webhook belongs to (from the service_id in the callback body) and
// that org's webhook-signing secret — see
// repository.ClickIntegrationRepository.FindByServiceIDForWebhook's doc
// comment for the RLS rationale.
type ClickIntegrationLookup interface {
	FindByServiceIDForWebhook(ctx context.Context, serviceID string) (*entity.ClickIntegration, error)
}

// Sender mirrors internal/usecase/ai's and internal/usecase/conversation's
// — see internal/usecase/ai.Sender's doc comment for why this package
// declares its own copy rather than importing either of theirs (usecases in
// this codebase don't depend on each other).
type Sender interface {
	SendMessage(ctx context.Context, accessToken, recipientIGID, text string) error
}

// TelegramSender mirrors internal/usecase/ai's.
type TelegramSender interface {
	SendMessage(ctx context.Context, botToken, businessConnectionID, chatID, text string) error
}

// TelegramAccountLookup mirrors internal/usecase/ai's.
type TelegramAccountLookup interface {
	FindByID(ctx context.Context, orgID, id uuid.UUID) (*entity.TelegramAccount, error)
}

type WebhookUseCase struct {
	clickIntegrations ClickIntegrationLookup
	orders            repository.OrderRepository
	convRepo          repository.ConversationRepository
	productRepo       repository.ProductRepository
	msgRepo           repository.MessageRepository
	accountRepo       repository.InstagramAccountRepository
	sender            Sender
	telegramAccounts  TelegramAccountLookup
	telegramSender    TelegramSender
	encryptor         *crypto.AESGCMEncryptor
	logger            *zap.Logger
}

func New(
	clickIntegrations ClickIntegrationLookup,
	orders repository.OrderRepository,
	convRepo repository.ConversationRepository,
	productRepo repository.ProductRepository,
	msgRepo repository.MessageRepository,
	accountRepo repository.InstagramAccountRepository,
	sender Sender,
	telegramAccounts TelegramAccountLookup,
	telegramSender TelegramSender,
	encryptor *crypto.AESGCMEncryptor,
	logger *zap.Logger,
) *WebhookUseCase {
	return &WebhookUseCase{
		clickIntegrations: clickIntegrations,
		orders:            orders,
		convRepo:          convRepo,
		productRepo:       productRepo,
		msgRepo:           msgRepo,
		accountRepo:       accountRepo,
		sender:            sender,
		telegramAccounts:  telegramAccounts,
		telegramSender:    telegramSender,
		encryptor:         encryptor,
		logger:            logger,
	}
}

// Process is the single entry point for both Prepare (action=0) and
// Complete (action=1) callbacks — Click posts both to the same merchant
// URL, distinguished only by the action field (see
// TelegramWebhookHandler's identical one-endpoint-many-update-types shape
// for the nearest precedent in this codebase). Always returns a
// Click-shaped response value (never a bare Go error) — see
// clickapi.PrepareResponse/CompleteResponse's doc comments — because
// Click's protocol communicates failure through the response body's error
// field at HTTP 200, not through HTTP status codes; the handler always
// writes 200 with whatever this returns.
func (uc *WebhookUseCase) Process(ctx context.Context, req clickapi.WebhookRequest) any {
	integration, err := uc.clickIntegrations.FindByServiceIDForWebhook(ctx, req.ServiceID)
	if err != nil {
		return errorResponse(req, clickapi.ErrOrderNotFound, "Service not found")
	}
	if integration.SecretKeyEncrypted == nil {
		// Connected before secret keys existed, or never finished setup —
		// see entity.ClickIntegration.SecretKeyEncrypted's doc comment.
		uc.logger.Warn("click webhook: integration has no secret key configured",
			zap.String("organization_id", integration.OrganizationID.String()))
		return errorResponse(req, clickapi.ErrBadRequest, "Integration not configured for payments")
	}
	secretKey, err := uc.encryptor.Decrypt(integration.SecretKeyEncrypted)
	if err != nil {
		uc.logger.Error("click webhook: decrypt secret key failed", zap.Error(err))
		return errorResponse(req, clickapi.ErrBadRequest, "Internal error")
	}
	if !req.VerifySignature(secretKey) {
		return errorResponse(req, clickapi.ErrSignCheckFailed, "SIGN CHECK FAILED!")
	}

	switch req.Action {
	case clickapi.ActionPrepare:
		return uc.handlePrepare(ctx, integration.OrganizationID, req)
	case clickapi.ActionComplete:
		return uc.handleComplete(ctx, integration.OrganizationID, req)
	default:
		return errorResponse(req, clickapi.ErrActionNotFound, "Action not found")
	}
}

func errorResponse(req clickapi.WebhookRequest, code int, note string) any {
	if req.Action == clickapi.ActionComplete {
		return clickapi.CompleteResponse{
			ClickTransID:    req.ClickTransID,
			MerchantTransID: req.MerchantTransID,
			Error:           code,
			ErrorNote:       note,
		}
	}
	return clickapi.PrepareResponse{
		ClickTransID:    req.ClickTransID,
		MerchantTransID: req.MerchantTransID,
		Error:           code,
		ErrorNote:       note,
	}
}

// handlePrepare creates the order the first time Click confirms a customer
// actually opened this checkout — see entity.Order's doc comment on why
// nothing is created earlier, when the link is merely built. Idempotent:
// re-preparing the same click_transaction_param (Click retried, or the
// customer reopened the same link) returns the existing order's id rather
// than erroring or duplicating.
func (uc *WebhookUseCase) handlePrepare(ctx context.Context, orgID uuid.UUID, req clickapi.WebhookRequest) clickapi.PrepareResponse {
	resp := clickapi.PrepareResponse{ClickTransID: req.ClickTransID, MerchantTransID: req.MerchantTransID}

	existing, err := uc.orders.FindByTransactionParam(ctx, orgID, req.MerchantTransID)
	switch {
	case err == nil:
		if existing.Status == entity.OrderStatusPaid {
			resp.Error = clickapi.ErrAlreadyPaid
			resp.ErrorNote = "Already paid"
			return resp
		}
		resp.MerchantPrepareID = existing.ID.String()
		resp.Error = clickapi.ErrSuccess
		resp.ErrorNote = "Success"
		return resp
	default:
		if ae, ok := apperror.As(err); !ok || ae.Code != apperror.CodeNotFound {
			uc.logger.Error("click webhook: lookup order by transaction param failed", zap.Error(err))
			resp.Error = clickapi.ErrBadRequest
			resp.ErrorNote = "Internal error"
			return resp
		}
	}

	conversationID, productID, parseErr := clickapi.ParseTransactionParam(req.MerchantTransID)
	if parseErr != nil {
		resp.Error = clickapi.ErrOrderNotFound
		resp.ErrorNote = "Order not found"
		return resp
	}

	conv, err := uc.convRepo.FindByID(ctx, orgID, conversationID)
	if err != nil {
		resp.Error = clickapi.ErrOrderNotFound
		resp.ErrorNote = "Order not found"
		return resp
	}
	product, err := uc.productRepo.FindByID(ctx, orgID, productID)
	if err != nil {
		resp.Error = clickapi.ErrOrderNotFound
		resp.ErrorNote = "Order not found"
		return resp
	}
	if !req.AmountMatches(product.PriceCents) {
		resp.Error = clickapi.ErrIncorrectAmount
		resp.ErrorNote = "Incorrect parameter amount"
		return resp
	}

	clickTransID := req.ClickTransID
	order := &entity.Order{
		OrganizationID:        orgID,
		ConversationID:        conv.ID,
		ProductID:             &product.ID,
		ProductNameSnapshot:   product.Name,
		AmountCents:           product.PriceCents,
		Currency:              product.Currency,
		Status:                entity.OrderStatusPending,
		ClickTransactionParam: req.MerchantTransID,
		ClickTransID:          &clickTransID,
	}
	if err := uc.orders.Create(ctx, order); err != nil {
		uc.logger.Error("click webhook: create order failed", zap.Error(err))
		resp.Error = clickapi.ErrBadRequest
		resp.ErrorNote = "Internal error"
		return resp
	}

	resp.MerchantPrepareID = order.ID.String()
	resp.Error = clickapi.ErrSuccess
	resp.ErrorNote = "Success"
	return resp
}

// handleComplete finalizes the order Prepare created (matched via
// merchant_prepare_id, which is this codebase's own order id — see
// clickapi.PrepareResponse's doc comment) and, only on a genuine first-time
// success, triggers the customer/admin notifications (see notifyPaid).
//
// req.Error < 0 means Click itself could not complete the charge on their
// end (insufficient funds, the user cancelled, etc.) — NOT that this
// merchant failed to process the notification. This codebase's official
// docs.click.uz reference for the exact acknowledgement contract in that
// branch was not reachable while building this (its API docs are a
// JS-rendered SPA — see clickapi's package doc comment on what was
// actually verified vs. reconstructed from a community reference). What's
// implemented here — record the order as failed, still acknowledge with
// error=0 since the notification itself was received and handled correctly
// — is the standard webhook-ack pattern most payment gateways use and
// matches the general shape of Click's own worked examples, but treat it
// as a best-effort interpretation, not a verified-against-primary-source
// one, if payments start behaving unexpectedly on declined charges.
func (uc *WebhookUseCase) handleComplete(ctx context.Context, orgID uuid.UUID, req clickapi.WebhookRequest) clickapi.CompleteResponse {
	resp := clickapi.CompleteResponse{ClickTransID: req.ClickTransID, MerchantTransID: req.MerchantTransID}

	orderID, err := uuid.Parse(req.MerchantPrepareID)
	if err != nil {
		resp.Error = clickapi.ErrTransactionNotFound
		resp.ErrorNote = "Transaction does not exist"
		return resp
	}
	order, err := uc.orders.FindByID(ctx, orgID, orderID)
	if err != nil {
		resp.Error = clickapi.ErrTransactionNotFound
		resp.ErrorNote = "Transaction does not exist"
		return resp
	}

	if order.Status == entity.OrderStatusPaid {
		// Click retried a Complete already processed — acknowledge without
		// re-sending the thank-you/system messages a second time.
		resp.MerchantConfirmID = order.ID.String()
		resp.Error = clickapi.ErrSuccess
		resp.ErrorNote = "Success"
		return resp
	}

	if req.Error < 0 {
		order.Status = entity.OrderStatusFailed
		if err := uc.orders.Update(ctx, order); err != nil {
			uc.logger.Error("click webhook: update failed order failed", zap.Error(err))
		}
		resp.MerchantConfirmID = order.ID.String()
		resp.Error = clickapi.ErrSuccess
		resp.ErrorNote = "Success"
		return resp
	}

	order.Status = entity.OrderStatusPaid
	now := time.Now()
	order.PaidAt = &now
	clickTransID := req.ClickTransID
	order.ClickTransID = &clickTransID
	if err := uc.orders.Update(ctx, order); err != nil {
		uc.logger.Error("click webhook: mark order paid failed", zap.Error(err))
		resp.Error = clickapi.ErrBadRequest
		resp.ErrorNote = "Internal error"
		return resp
	}

	uc.notifyPaid(ctx, orgID, order)

	resp.MerchantConfirmID = order.ID.String()
	resp.Error = clickapi.ErrSuccess
	resp.ErrorNote = "Success"
	return resp
}

// notifyPaid is the whole point of this package, per its doc comment: once
// money has actually been confirmed received, tell the customer (a plain
// thank-you, over whichever channel — Instagram or Telegram — they were
// already talking through) and tell the org (a system message in that same
// conversation naming the exact product/service and amount, so whoever
// reads the inbox knows what to fulfil, without needing a separate Orders
// page — see entity.Message.SenderType's "system" value, previously
// declared but unused anywhere in this codebase until now). Best-effort:
// the payment itself is already durably recorded (order.Status was set to
// paid before this is called), so a Meta/Telegram API hiccup here must not
// turn a successful payment into a Complete-callback failure — swallowed
// and logged instead, same posture as buildProductContext's Click lookup.
func (uc *WebhookUseCase) notifyPaid(ctx context.Context, orgID uuid.UUID, order *entity.Order) {
	conv, err := uc.convRepo.FindByID(ctx, orgID, order.ConversationID)
	if err != nil {
		uc.logger.Warn("click webhook: load conversation for paid-order notification failed",
			zap.String("order_id", order.ID.String()), zap.Error(err))
		return
	}

	amountText := formatSom(order.AmountCents)

	thankYou := fmt.Sprintf(
		"Rahmat! To'lovingiz Click orqali muvaffaqiyatli qabul qilindi ✅\n%s — %s so'm.",
		order.ProductNameSnapshot, amountText,
	)
	if err := uc.sendToCustomer(ctx, orgID, conv, thankYou); err != nil {
		uc.logger.Warn("click webhook: send thank-you message failed",
			zap.String("order_id", order.ID.String()), zap.Error(err))
	} else if err := uc.recordMessage(ctx, orgID, conv, entity.MessageSenderAI, thankYou, nil); err != nil {
		uc.logger.Warn("click webhook: persist thank-you message failed",
			zap.String("order_id", order.ID.String()), zap.Error(err))
	}

	adminNote := fmt.Sprintf(
		"\U0001F4B0 To'lov qabul qilindi (Click)\nMahsulot/xizmat: %s\nSumma: %s so'm\nMijozga tasdiqlash xabari yuborildi — buyurtmani yetkazib berish yoki xizmatni ko'rsatishni unutmang.",
		order.ProductNameSnapshot, amountText,
	)
	metadata := map[string]any{
		"order_id":     order.ID.String(),
		"amount_cents": order.AmountCents,
	}
	if order.ProductID != nil {
		metadata["product_id"] = order.ProductID.String()
	}
	if err := uc.recordMessage(ctx, orgID, conv, entity.MessageSenderSystem, adminNote, metadata); err != nil {
		uc.logger.Warn("click webhook: persist admin system message failed",
			zap.String("order_id", order.ID.String()), zap.Error(err))
	}
}

// sendToCustomer mirrors internal/usecase/ai.UseCase's and
// internal/usecase/conversation.UseCase's identical channel switch — see
// internal/usecase/ai.HandleInboundMessage's doc comment on why this
// branches on conv.Channel. Duplicated, not shared, for the same
// "usecases don't depend on each other" reason as Sender/TelegramSender
// above.
func (uc *WebhookUseCase) sendToCustomer(ctx context.Context, orgID uuid.UUID, conv *entity.Conversation, text string) error {
	switch conv.Channel {
	case entity.ConversationChannelTelegram:
		if conv.TelegramAccountID == nil {
			return errors.New("conversation has channel=telegram but no telegram_account_id")
		}
		account, err := uc.telegramAccounts.FindByID(ctx, orgID, *conv.TelegramAccountID)
		if err != nil {
			return err
		}
		if account.BusinessConnectionID == nil {
			return errors.New("telegram account has no business_connection_id")
		}
		botToken, err := uc.encryptor.Decrypt(account.BotTokenEncrypted)
		if err != nil {
			return err
		}
		return uc.telegramSender.SendMessage(ctx, botToken, *account.BusinessConnectionID, conv.CustomerIGID, text)
	default:
		if conv.InstagramAccountID == nil {
			return errors.New("conversation has channel=instagram but no instagram_account_id")
		}
		account, err := uc.accountRepo.FindByID(ctx, orgID, *conv.InstagramAccountID)
		if err != nil {
			return err
		}
		accessToken, err := uc.encryptor.Decrypt(account.AccessTokenEncrypted)
		if err != nil {
			return err
		}
		return uc.sender.SendMessage(ctx, accessToken, conv.CustomerIGID, text)
	}
}

// recordMessage persists one outbound message and bumps the conversation's
// list-view fields (LastMessageAt/Preview, UnreadCount) — the same two
// steps internal/usecase/ai.HandleInboundMessage and
// internal/usecase/conversation.UseCase.SendMessage each do after their own
// send call. Unlike those, this is called twice per paid order (thank-you,
// then the admin system message) — UnreadCount increments both times
// deliberately, so a payment surfaces in the inbox the same way a genuinely
// new customer message would.
func (uc *WebhookUseCase) recordMessage(ctx context.Context, orgID uuid.UUID, conv *entity.Conversation, senderType entity.MessageSenderType, content string, metadata map[string]any) error {
	msg := &entity.Message{
		OrganizationID: orgID,
		ConversationID: conv.ID,
		Direction:      entity.MessageDirectionOutbound,
		SenderType:     senderType,
		MessageType:    entity.MessageTypeText,
		Content:        &content,
		Metadata:       metadata,
	}
	if err := uc.msgRepo.Create(ctx, msg); err != nil {
		return err
	}

	preview := content
	if len(preview) > maxNotifyPreviewLen {
		preview = preview[:maxNotifyPreviewLen]
	}
	conv.LastMessageAt = &msg.CreatedAt
	conv.LastMessagePreview = &preview
	conv.UnreadCount++
	return uc.convRepo.Update(ctx, conv)
}

// maxNotifyPreviewLen mirrors internal/usecase/ai's maxReplyPreviewLen
// (same purpose, not imported for the same "usecases don't depend on each
// other" reason).
const maxNotifyPreviewLen = 140

// formatSom mirrors internal/usecase/ai's function of the same name
// exactly — see that package's doc comment on why UZS amounts render
// without decimals when the tiyin remainder is zero. Duplicated, not
// shared, for the same reason as everything else in this file.
func formatSom(priceCents int64) string {
	som := priceCents / 100
	remainder := priceCents % 100
	if remainder == 0 {
		return fmt.Sprintf("%d", som)
	}
	return fmt.Sprintf("%d.%02d", som, remainder)
}
