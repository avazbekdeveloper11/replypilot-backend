-- Products: an organization's own sellable-item catalog, distinct from
-- knowledge_base_documents (unstructured RAG source text). The AI reply
-- pipeline reads active products directly (structured, not embedding
-- retrieval) to quote exact prices and build Click payment links.
CREATE TABLE products (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text,
    price_cents     bigint NOT NULL CHECK (price_cents >= 0),
    currency        text NOT NULL DEFAULT 'UZS',
    is_active       boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE INDEX idx_products_org ON products(organization_id) WHERE deleted_at IS NULL;
-- Feeds internal/usecase/ai's per-message product-context lookup — every
-- inbound message hits this, so it's indexed for the exact WHERE clause
-- ListActiveByOrganization uses.
CREATE INDEX idx_products_org_active ON products(organization_id)
    WHERE deleted_at IS NULL AND is_active = true;

CREATE TRIGGER trg_products_updated_at
    BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Click (click.uz) integration: one row per organization. merchant_id and
-- service_id are Click's own public merchant/service identifiers (the same
-- values a plain HTML payment button on the merchant's own website would
-- use — see docs.click.uz/en/click-button) — not secrets, so no encryption
-- column here, unlike instagram_accounts.access_token_encrypted.
CREATE TABLE click_integrations (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    merchant_id          text NOT NULL,
    service_id           text NOT NULL,
    merchant_user_id     text,
    connected_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    deleted_at           timestamptz
);

-- At most one active connection per org — ProductRepository.Upsert relies
-- on this to decide insert-vs-replace.
CREATE UNIQUE INDEX uq_click_integrations_org ON click_integrations(organization_id) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_click_integrations_updated_at
    BEFORE UPDATE ON click_integrations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Same tenant-isolation RLS pattern as every other per-org table (see
-- 000001_init_schema.up.sql's big DO $$ loop) — these two tables didn't
-- exist yet when that loop ran, so they're enabled here individually
-- rather than editing an already-applied migration.
ALTER TABLE products ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON products
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);

ALTER TABLE click_integrations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON click_integrations
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);
