-- Leads: a customer who left a phone number in an Instagram DM — see
-- entity.Lead's doc comment for why this is its own table/state machine
-- rather than reusing conversations.status. Captured automatically by the
-- AI reply pipeline (internal/usecase/ai), never entered by hand.
CREATE TABLE leads (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    phone           text NOT NULL,
    summary         text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'new',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Feeds two query shapes: the Leads page's default "new" filter, and
-- ai.UseCase's per-message HasOpen check (organization_id + conversation_id
-- + status='new') that guards against creating a duplicate lead on every
-- subsequent message once one is already open.
CREATE INDEX idx_leads_org_status ON leads(organization_id, status);
CREATE INDEX idx_leads_conversation_status ON leads(conversation_id, status);

CREATE TRIGGER trg_leads_updated_at
    BEFORE UPDATE ON leads
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Same tenant-isolation RLS pattern as every other per-org table added
-- after migration 000001's baseline loop (see 000011's identical note).
ALTER TABLE leads ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON leads
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);
