-- Migration 000002 — seed reference data (DOWN)
-- Reverses the seed inserts. Deleting the system roles cascades to
-- role_permissions (FK ON DELETE CASCADE), so those rows go automatically.
--
-- NOTE: this only removes the seeded reference rows. It does NOT attempt to
-- delete tenant data that may reference these rows (e.g. a team_member
-- pointing at the Owner role) — team_members.role_id is ON DELETE RESTRICT,
-- so if any tenant is live this DELETE will (correctly) fail rather than
-- silently orphan memberships. Roll back 000002 only against a database with
-- no live tenants.

DELETE FROM plans WHERE code IN ('starter', 'pro', 'enterprise');

-- Cascades to role_permissions for these roles.
DELETE FROM roles WHERE organization_id IS NULL
    AND name IN ('Owner', 'Admin', 'Agent', 'Viewer');

DELETE FROM permissions WHERE key IN (
    'conversations.read', 'conversations.write', 'conversations.handoff',
    'knowledge_base.read', 'knowledge_base.write',
    'team.manage', 'roles.manage',
    'billing.read', 'billing.manage',
    'analytics.read', 'settings.manage'
);
