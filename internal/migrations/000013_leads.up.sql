-- dm_leads: a customer who left a phone number in an Instagram DM — see
-- entity.Lead's doc comment for why this is its own table/state machine
-- rather than reusing conversations.status. Captured automatically by the
-- AI reply pipeline (internal/usecase/ai), never entered by hand.
--
-- Named dm_leads, not leads: migration 000001 already defines an unrelated
-- "leads" table (CRM-style: full_name/email/estimated_value_cents/
-- lead_status_enum) that predates this feature and has no Go code behind
-- it. Reusing that name collided outright (CREATE TABLE leads failed with
-- "relation already exists" on every environment). Rather than repurpose
-- or drop dead schema that isn't ours to remove unilaterally, this table
-- gets its own name.
CREATE TABLE dm_leads (
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
CREATE INDEX idx_dm_leads_org_status ON dm_leads(organization_id, status);
CREATE INDEX idx_dm_leads_conversation_status ON dm_leads(conversation_id, status);

CREATE TRIGGER trg_dm_leads_updated_at
    BEFORE UPDATE ON dm_leads
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Same tenant-isolation RLS pattern as every other per-org table added
-- after migration 000001's baseline loop (see 000011's identical note).
ALTER TABLE dm_leads ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON dm_leads
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);
