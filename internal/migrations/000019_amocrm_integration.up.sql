-- amoCRM integration: lets an org connect its own amoCRM account (OAuth
-- 2.0, client_id/client_secret are ReplyPilot's own platform-level app
-- credentials — internal/config's AmoCRMConfig — shared across every
-- org, same shape as Meta's app credentials for Instagram) and push its
-- customers as amoCRM contacts, with a note summarizing what they've
-- bought. amoCRM is the most-used CRM in this market; this is a
-- one-way (ReplyPilot -> amoCRM) manual/bulk sync for v1, not a
-- real-time bidirectional integration or a full deal/pipeline sync —
-- see internal/usecase/amocrm's package doc comment for the exact scope.

CREATE TYPE amocrm_integration_status_enum AS ENUM ('connected', 'expired', 'revoked', 'error');

-- --- amocrm_integrations: one row per org's amoCRM connection ---
--
-- Same shape as click_integrations (migration 000011): a real uuid PK
-- (not organization_id itself), soft-deleted on disconnect, at most one
-- non-deleted row per org enforced by the partial unique index below —
-- postgres.AmoCRMRepository.Upsert relies on this the same way
-- ClickIntegrationRepository.Upsert does.
--
-- Unlike ClickIntegration (which has no token to encrypt — see that
-- entity's doc comment), this needs BOTH an access token (24h lifespan)
-- AND a refresh token (3-month lifespan, rotates on every use — see
-- https://developers.kommo.com/docs/oauth-20) encrypted at rest with
-- the same AES-256-GCM envelope encryption already used for Instagram
-- access tokens (pkg/crypto, cfg.Security.TokenEncryptionKey — no new
-- key needed).
CREATE TABLE amocrm_integrations (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- The amoCRM account's own subdomain (e.g. "example" for
    -- example.amocrm.ru) — every API call and the token refresh call
    -- itself are per-subdomain, not a global endpoint. Resolved from the
    -- `referer` query param amoCRM's OAuth redirect sends back, not
    -- asked of the user up front (see usecase/amocrm/oauth_usecase.go).
    subdomain               text NOT NULL,
    access_token_encrypted  bytea NOT NULL,
    refresh_token_encrypted bytea NOT NULL,
    access_token_expires_at timestamptz NOT NULL,
    status                  amocrm_integration_status_enum NOT NULL DEFAULT 'connected',
    connected_by_user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz
);

CREATE UNIQUE INDEX uq_amocrm_integrations_org ON amocrm_integrations(organization_id) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_amocrm_integrations_updated_at
    BEFORE UPDATE ON amocrm_integrations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE amocrm_integrations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON amocrm_integrations
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);

-- --- amocrm_contact_links: which amoCRM contact a conversation maps to ---
--
-- Exists purely so re-syncing the same customer updates their existing
-- amoCRM contact in place instead of creating a duplicate every time —
-- amoCRM's API has no "upsert by external id" for contacts, so this
-- table IS that external-id mapping, kept on our side. No RLS bypass
-- policy needed: unlike webhook-driven tables (click_integrations,
-- telegram_accounts), every read/write here happens inside an
-- already-authenticated, already-org-scoped request (a dashboard user
-- clicking "Sync to amoCRM"), same reasoning as migration 000015's
-- orders table.
CREATE TABLE amocrm_contact_links (
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    conversation_id  uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    amocrm_contact_id bigint NOT NULL,
    synced_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, conversation_id)
);

ALTER TABLE amocrm_contact_links ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON amocrm_contact_links
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);
