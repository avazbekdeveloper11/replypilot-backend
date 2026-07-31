-- Migration 000008 — platform-admin role + cross-tenant read access (UP)
--
-- ReplyPilot's own staff (not a tenant org's Owner/Admin — that system is
-- per-org team_members/roles, unrelated) need to see across all tenants:
-- an admin panel listing every organization, and platform-wide aggregate
-- stats (total conversations, total messages, active subscriptions). This
-- is a single boolean flag on users, not a new roles/permissions system —
-- there is exactly one capability here ("is ReplyPilot staff"), not a
-- graduated set of platform-level permissions to model.
--
-- users and organizations have NO row-level security (see migration
-- 000001 §17 — they are absent from the tenant_isolation loop), so
-- listing all orgs/users needs no policy change at all. This migration
-- only adds read access to the RLS-protected tables an admin panel
-- actually needs to aggregate: team_members (member counts per org),
-- conversations and messages (platform-wide counts), and subscriptions
-- (active-subscription / approximate-MRR stats).
--
-- Same GUC-gated PERMISSIVE-policy pattern as migration 000003's
-- webhook_lookup and 000007's billing webhook_lookup: the application sets
-- `SET LOCAL app.platform_admin = 'on'` (transaction-scoped, auto-cleared
-- on commit) around exactly the platform-admin repository's queries — see
-- internal/repository/postgres/platform_admin.go's withPlatformAdmin
-- helper. RLS policies are OR-combined, so this adds a narrowly-scoped
-- allowed path without weakening tenant_isolation for any normal query.
-- It is deliberately SELECT-only on all four tables — no write bypass.

ALTER TABLE users ADD COLUMN is_platform_admin BOOLEAN NOT NULL DEFAULT false;

CREATE POLICY platform_admin_read ON team_members
    FOR SELECT
    USING (current_setting('app.platform_admin', true) = 'on');

CREATE POLICY platform_admin_read ON conversations
    FOR SELECT
    USING (current_setting('app.platform_admin', true) = 'on');

CREATE POLICY platform_admin_read ON messages
    FOR SELECT
    USING (current_setting('app.platform_admin', true) = 'on');

CREATE POLICY platform_admin_read ON subscriptions
    FOR SELECT
    USING (current_setting('app.platform_admin', true) = 'on');
