-- Migration 000008 — platform-admin role + cross-tenant read access (DOWN)

DROP POLICY IF EXISTS platform_admin_read ON subscriptions;
DROP POLICY IF EXISTS platform_admin_read ON messages;
DROP POLICY IF EXISTS platform_admin_read ON conversations;
DROP POLICY IF EXISTS platform_admin_read ON team_members;

ALTER TABLE users DROP COLUMN is_platform_admin;
