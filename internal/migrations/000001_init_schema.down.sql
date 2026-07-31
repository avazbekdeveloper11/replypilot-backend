-- Migration 000001 — init schema (DOWN)
-- Reverses 000001_init_schema.up.sql. Drops everything the baseline created,
-- in reverse dependency order. CASCADE on the tables takes care of foreign
-- keys, RLS policies, partitions, and dependent indexes, so those don't need
-- to be listed individually.
--
-- Partitioned tables (messages, webhook_logs, audit_logs): dropping the
-- parent with CASCADE drops all child partitions too — no need to enumerate
-- messages_2026_07, _08, etc.

-- Tables (child/dependent first, though CASCADE makes strict order optional).
DROP TABLE IF EXISTS notification_channels CASCADE;
DROP TABLE IF EXISTS audit_logs CASCADE;
DROP TABLE IF EXISTS webhook_logs CASCADE;
DROP TABLE IF EXISTS usage_records CASCADE;
DROP TABLE IF EXISTS invoices CASCADE;
DROP TABLE IF EXISTS subscriptions CASCADE;
DROP TABLE IF EXISTS plans CASCADE;
DROP TABLE IF EXISTS ai_token_usage CASCADE;
DROP TABLE IF EXISTS ai_response_citations CASCADE;
DROP TABLE IF EXISTS ai_responses CASCADE;
DROP TABLE IF EXISTS knowledge_base_chunks CASCADE;
DROP TABLE IF EXISTS knowledge_base_documents CASCADE;
DROP TABLE IF EXISTS messages CASCADE;
DROP TABLE IF EXISTS leads CASCADE;
DROP TABLE IF EXISTS conversation_tags CASCADE;
DROP TABLE IF EXISTS conversations CASCADE;
DROP TABLE IF EXISTS tags CASCADE;
DROP TABLE IF EXISTS instagram_accounts CASCADE;
DROP TABLE IF EXISTS team_members CASCADE;
DROP TABLE IF EXISTS role_permissions CASCADE;
DROP TABLE IF EXISTS roles CASCADE;
DROP TABLE IF EXISTS permissions CASCADE;
DROP TABLE IF EXISTS organizations CASCADE;
DROP TABLE IF EXISTS users CASCADE;

-- Trigger function (all triggers dropped with their tables above).
DROP FUNCTION IF EXISTS set_updated_at();

-- Enum types.
DROP TYPE IF EXISTS notification_channel_type_enum;
DROP TYPE IF EXISTS webhook_status_enum;
DROP TYPE IF EXISTS webhook_source_enum;
DROP TYPE IF EXISTS usage_metric_type_enum;
DROP TYPE IF EXISTS invoice_status_enum;
DROP TYPE IF EXISTS subscription_status_enum;
DROP TYPE IF EXISTS lead_status_enum;
DROP TYPE IF EXISTS kb_document_status_enum;
DROP TYPE IF EXISTS kb_source_type_enum;
DROP TYPE IF EXISTS message_type_enum;
DROP TYPE IF EXISTS message_sender_type_enum;
DROP TYPE IF EXISTS message_direction_enum;
DROP TYPE IF EXISTS conversation_status_enum;
DROP TYPE IF EXISTS instagram_account_status_enum;
DROP TYPE IF EXISTS team_member_status_enum;
DROP TYPE IF EXISTS user_status_enum;
DROP TYPE IF EXISTS organization_status_enum;

-- Extensions are intentionally NOT dropped. Other databases in the same
-- cluster may depend on them, and re-creating them on the next `up` is
-- cheap and idempotent (CREATE EXTENSION IF NOT EXISTS). Dropping vector /
-- citext / pgcrypto here would be an unexpected side effect of a schema
-- rollback. Remove this comment and add DROP EXTENSION lines only if this
-- database is guaranteed single-tenant-to-this-app.
