-- Postgres has no DROP VALUE for enums — 'click' stays in webhook_source_enum
-- on rollback (harmless: same trade-off already accepted for every other
-- ADD VALUE in this codebase's migration history).

DROP TABLE IF EXISTS orders;
DROP TYPE IF EXISTS order_status_enum;

DROP POLICY IF EXISTS webhook_account_lookup ON click_integrations;
DROP INDEX IF EXISTS uq_click_integrations_service_id;
ALTER TABLE click_integrations DROP COLUMN IF EXISTS secret_key_encrypted;
