-- Admin-facing Telegram notifications: the same already-connected bot
-- (telegram_accounts — see migration 000014's header comment) can also DM
-- the org's admin directly (plain sendMessage, no business_connection_id)
-- whenever a new lead is captured or a payment completes, independent of
-- whether Business Bot pairing has happened at all.
--
-- notify_chat_id is only ever set by a verification-code handshake, not
-- typed in by the org directly: binding a stranger's chat_id here would let
-- them silently receive every future lead/payment notification for this
-- org's bot, since a bot's @username is not a secret. The flow (see
-- telegram.ConnectUseCase.GenerateNotifyCode and
-- telegram.WebhookUseCase's plain-message handling): the admin generates
-- notify_verify_code in Settings, sends that exact text to the bot as a
-- normal Telegram message, and only that match binds notify_chat_id to the
-- sender's chat id.
ALTER TABLE telegram_accounts
    ADD COLUMN notify_chat_id     bigint,
    ADD COLUMN notify_verify_code text,
    ADD COLUMN notify_on_lead     boolean NOT NULL DEFAULT true,
    ADD COLUMN notify_on_payment  boolean NOT NULL DEFAULT true;
