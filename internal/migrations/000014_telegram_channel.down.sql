COMMENT ON COLUMN messages.ig_message_id IS 'Meta''s message id; used for idempotent ingestion';

DROP INDEX IF EXISTS idx_conversations_telegram_account;
DROP INDEX IF EXISTS uq_conversations_telegram_account_customer;
DROP INDEX IF EXISTS uq_conversations_instagram_account_customer;
CREATE UNIQUE INDEX uq_conversations_account_customer
    ON conversations(instagram_account_id, customer_ig_id) WHERE deleted_at IS NULL;

COMMENT ON COLUMN conversations.customer_ig_id IS 'Meta''s scoped id for the customer';

ALTER TABLE conversations
    DROP CONSTRAINT IF EXISTS chk_conversations_channel_account,
    DROP COLUMN IF EXISTS telegram_account_id,
    DROP COLUMN IF EXISTS channel,
    ALTER COLUMN instagram_account_id SET NOT NULL;

DROP TABLE IF EXISTS telegram_accounts;
DROP TYPE IF EXISTS telegram_account_status_enum;
DROP TYPE IF EXISTS conversation_channel_enum;
