-- Migration 000002 — seed reference data (UP)
-- Permissions, system roles (Owner/Admin/Agent/Viewer), their permission
-- mappings, and default plans. Separated from 000001 because this is
-- reference data the application needs to function, not schema structure —
-- it versions and rolls back independently.

-- SECTION 18: SEED DATA — permissions, system roles, default plans
-- =============================================================================

INSERT INTO permissions (key, category, description) VALUES
    ('conversations.read',   'conversations',    'View conversations and message history'),
    ('conversations.write',  'conversations',    'Send messages, tag, assign conversations'),
    ('conversations.handoff','conversations',    'Take over a conversation from the AI'),
    ('knowledge_base.read',  'knowledge_base',   'View knowledge base documents'),
    ('knowledge_base.write', 'knowledge_base',   'Upload, edit, delete knowledge base documents'),
    ('team.manage',          'team',             'Invite, remove, and reassign roles for team members'),
    ('roles.manage',         'team',             'Create and edit custom roles'),
    ('billing.read',         'billing',          'View subscription and invoices'),
    ('billing.manage',       'billing',          'Change plan, update payment method'),
    ('analytics.read',       'analytics',        'View analytics dashboards'),
    ('settings.manage',      'settings',         'Manage organization and integration settings');

INSERT INTO roles (organization_id, name, description, is_system) VALUES
    (NULL, 'Owner',  'Full access, including billing and organization deletion', true),
    (NULL, 'Admin',  'Full access except billing', true),
    (NULL, 'Agent',  'Handles conversations and knowledge base, no team/billing access', true),
    (NULL, 'Viewer', 'Read-only access', true);

WITH r AS (SELECT id, name FROM roles WHERE organization_id IS NULL),
     p AS (SELECT id, key FROM permissions)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM r, p WHERE r.name = 'Owner'
UNION ALL
SELECT r.id, p.id FROM r, p WHERE r.name = 'Admin' AND p.key NOT LIKE 'billing.%'
UNION ALL
SELECT r.id, p.id FROM r, p WHERE r.name = 'Agent'
    AND p.key IN ('conversations.read', 'conversations.write', 'conversations.handoff', 'knowledge_base.read', 'analytics.read')
UNION ALL
SELECT r.id, p.id FROM r, p WHERE r.name = 'Viewer'
    AND p.key IN ('conversations.read', 'knowledge_base.read', 'billing.read', 'analytics.read');

INSERT INTO plans (code, name, price_monthly_cents, price_yearly_cents, message_limit, seat_limit, features) VALUES
    ('starter',    'Starter',    4900,  49900,  1000,  2,   '{"kb_documents_limit": 5}'::jsonb),
    ('pro',        'Pro',        14900, 149900, 10000, 10,  '{"kb_documents_limit": 50}'::jsonb),
    ('enterprise', 'Enterprise', 0,     0,      NULL,  NULL,'{"kb_documents_limit": null, "custom_pricing": true}'::jsonb);
