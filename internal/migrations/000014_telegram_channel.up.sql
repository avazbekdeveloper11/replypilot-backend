-- Telegram as a second inbound channel, alongside Instagram.
--
-- Uses Telegram's "Business Bot" feature (Bot API 7.2+): a bot is connected
-- to a real person's Telegram Business account from inside their own
-- Telegram app (Settings -> Telegram Business -> Chatbots — a Telegram
-- Premium feature). Once connected, Telegram delivers that account's DMs to
-- our bot's webhook as business_message updates carrying a
-- business_connection_id, and replies are sent back through the same
-- Bot API with that id attached, appearing as if sent from the human's own
-- account. There is no OAuth here (unlike Instagram) — connecting is just
-- pasting a bot token created via @BotFather; see telegram.ConnectUseCase.
--
-- Design choice: conversations/messages stay ONE shared pair of tables for
-- both channels rather than a parallel telegram_conversations/
-- telegram_messages pair. The alternative (fully separate tables) would
-- force internal/usecase/ai's entire RAG/generation/lead-capture pipeline
-- to be duplicated per channel; sharing the tables lets that whole pipeline
-- (everything except the final "send the reply" step) stay channel-agnostic
-- with zero changes. The trade-off: instagram_account_id must become
-- nullable and customer_ig_id/ig_message_id are reused generically for
-- Telegram's chat id / message id (as strings) rather than adding
-- Telegram-named twin columns — see the column comments below.

CREATE TYPE conversation_channel_enum AS ENUM ('instagram', 'telegram');

-- No "expired" status here unlike instagram_account_status_enum: a bot
-- token created via BotFather does not expire the way an Instagram OAuth
-- token does. 'error' covers a token BotFather revoked or a send that
-- started failing for any other reason.
CREATE TYPE telegram_account_status_enum AS ENUM ('connected', 'error');

CREATE TABLE telegram_accounts (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    bot_token_encrypted     bytea NOT NULL,          -- envelope-encrypted at app layer, same as instagram_accounts
    bot_username            text,                    -- from Bot API getMe, for display only
    -- Set the first time Telegram delivers a business_connection update for
    -- this bot (i.e. once the org actually finishes pairing it in their own
    -- Telegram app's Chat Automation screen) — null between "token saved"
    -- and "pairing completed". Required to send: every outbound sendMessage
    -- call needs it.
    business_connection_id  text,
    status                  telegram_account_status_enum NOT NULL DEFAULT 'connected',
    connected_by_user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz
);

CREATE UNIQUE INDEX uq_telegram_accounts_business_connection_id
    ON telegram_accounts(business_connection_id) WHERE business_connection_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX idx_telegram_accounts_org ON telegram_accounts(organization_id) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_telegram_accounts_updated_at
    BEFORE UPDATE ON telegram_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Same tenant-isolation RLS pattern as every other per-org table added
-- after migration 000001's baseline loop (see 000011/000013's identical
-- note).
ALTER TABLE telegram_accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON telegram_accounts
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);

-- Mirrors migration 000003's webhook_account_lookup policy on
-- instagram_accounts, same app.webhook_lookup GUC (a session variable, not
-- a table — reusing its name across both tables' policies is intentional,
-- not a collision). Set only by TelegramAccountRepository's webhook-path
-- lookup method; see that file for the full rationale, already documented
-- once in migration 000003 rather than repeated here.
CREATE POLICY webhook_account_lookup ON telegram_accounts
    FOR SELECT
    USING (current_setting('app.webhook_lookup', true) = 'on');


-- --- conversations: make instagram_account_id optional, add a channel
-- --- discriminator and the Telegram-side account FK ---

ALTER TABLE conversations
    ALTER COLUMN instagram_account_id DROP NOT NULL,
    ADD COLUMN channel conversation_channel_enum NOT NULL DEFAULT 'instagram',
    ADD COLUMN telegram_account_id uuid REFERENCES telegram_accounts(id) ON DELETE CASCADE,
    ADD CONSTRAINT chk_conversations_channel_account CHECK (
        (channel = 'instagram' AND instagram_account_id IS NOT NULL AND telegram_account_id IS NULL) OR
        (channel = 'telegram' AND telegram_account_id IS NOT NULL AND instagram_account_id IS NULL)
    );

-- customer_ig_id is reused as-is for Telegram rows (holding the customer's
-- Telegram chat id, formatted as a plain decimal string) rather than adding
-- a customer_telegram_chat_id twin column — see this file's header comment.
-- Every existing row keeps working unchanged; only new Telegram-channel
-- rows populate it with a different kind of value than its name suggests.
COMMENT ON COLUMN conversations.customer_ig_id IS
    'Customer''s per-channel external id: Instagram IGSID for channel=instagram, Telegram chat_id (as a decimal string) for channel=telegram.';

DROP INDEX uq_conversations_account_customer;
CREATE UNIQUE INDEX uq_conversations_instagram_account_customer
    ON conversations(instagram_account_id, customer_ig_id) WHERE deleted_at IS NULL AND channel = 'instagram';
CREATE UNIQUE INDEX uq_conversations_telegram_account_customer
    ON conversations(telegram_account_id, customer_ig_id) WHERE deleted_at IS NULL AND channel = 'telegram';

CREATE INDEX idx_conversations_telegram_account ON conversations(telegram_account_id) WHERE deleted_at IS NULL;

-- messages.ig_message_id is reused the same way for Telegram's message_id
-- (also formatted as a decimal string) — it was already nullable with a
-- partial unique index (WHERE ig_message_id IS NOT NULL), which is exactly
-- the idempotency behavior Telegram ingestion needs too, so no schema
-- change is needed on the (partitioned) messages table at all.
COMMENT ON COLUMN messages.ig_message_id IS
    'Source message id for idempotent ingestion: Meta''s mid for channel=instagram, Telegram''s message_id (as a decimal string) for channel=telegram.';
