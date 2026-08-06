-- Click payment confirmation: turns internal/integration/clickapi's
-- link-generation-only MVP (see that package's pre-existing doc comment)
-- into a real payment flow. Click's Shop API two-phase webhook (Prepare,
-- then Complete — https://docs.click.uz/en/merchant-api-request/) needs (a)
-- a per-org secret key to verify the MD5 signature on every callback, since
-- merchant_id/service_id alone are public and unauthenticated, and (b) an
-- orders table to actually track "was this paid, and for what" — neither
-- existed before this migration.

-- --- click_integrations: add the secret key, and make lookup-by-service_id
-- --- possible for the webhook (which arrives with no org/session context,
-- --- only Click's own service_id identifying which org it's for) ---

ALTER TABLE click_integrations
    ADD COLUMN secret_key_encrypted bytea;

-- service_id must resolve to exactly one org for the webhook to know whose
-- secret key to verify against. Partial (deleted_at IS NULL) so a
-- disconnect-then-reconnect-with-the-same-service_id sequence never
-- collides with its own soft-deleted predecessor.
CREATE UNIQUE INDEX uq_click_integrations_service_id
    ON click_integrations(service_id) WHERE deleted_at IS NULL;

-- Same SET LOCAL app.webhook_lookup pattern as migration 000014's
-- webhook_account_lookup policy on telegram_accounts (see that file's
-- comment for the full RLS rationale) — Click's Prepare/Complete callback
-- has no org context until this exact lookup resolves one, so a permissive
-- SELECT-only policy scoped to that one lookup call is required. Reusing
-- the same GUC name across tables is intentional, not a collision — see
-- migration 000014's identical note.
CREATE POLICY webhook_account_lookup ON click_integrations
    FOR SELECT
    USING (current_setting('app.webhook_lookup', true) = 'on');

-- --- orders: one row per product a customer actually starts paying for ---
--
-- Deliberately NOT created when a payment link is built (internal/usecase/ai's
-- buildProductContext runs on every single inbound message and would
-- otherwise fan out up to maxProductsInPrompt rows per AI reply for links
-- nobody ever clicks). An order is created lazily, inside the Prepare step
-- of Click's webhook — i.e. the first moment Click itself confirms a real
-- checkout actually started. click_transaction_param
-- ("{conversation_id}-{product_id}", already how buildProductContext builds
-- Click's transaction_param today) is stable and deterministic per
-- conversation+product, so re-generating the same link twice in the same
-- conversation still resolves to one order.
CREATE TYPE order_status_enum AS ENUM ('pending', 'paid', 'failed', 'cancelled');

CREATE TABLE orders (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    conversation_id        uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    product_id             uuid REFERENCES products(id) ON DELETE SET NULL,
    -- Frozen at order-creation time so the admin (and the paid-confirmation
    -- messages) still show the right product name/price even if the
    -- product is later renamed, re-priced, or deleted.
    product_name_snapshot  text NOT NULL,
    amount_cents           bigint NOT NULL CHECK (amount_cents >= 0),
    currency               text NOT NULL DEFAULT 'UZS',
    status                 order_status_enum NOT NULL DEFAULT 'pending',
    click_transaction_param text NOT NULL,
    click_trans_id         bigint,
    paid_at                timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

-- One order per (conversation, product) pair — see the table comment above
-- on why click_transaction_param is deterministic, not a fresh id per link.
CREATE UNIQUE INDEX uq_orders_transaction_param ON orders(click_transaction_param);
CREATE INDEX idx_orders_conversation ON orders(conversation_id);
CREATE INDEX idx_orders_org ON orders(organization_id);

CREATE TRIGGER trg_orders_updated_at
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Standard tenant-isolation policy only — unlike click_integrations/
-- telegram_accounts/instagram_accounts, orders is never queried before an
-- org is already known: payment.WebhookUseCase resolves the org from
-- click_integrations' webhook_account_lookup policy FIRST, then does every
-- order read/write inside that resolved org's normal tenant-scoped
-- transaction (see postgres.OrderRepository). No bypass policy needed here.
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON orders
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);

-- New webhook source for the audit-trail table every other webhook receiver
-- already logs into (see entity.WebhookLog's doc comment). Must run in its
-- own statement outside anything that also uses the new value in the same
-- transaction — nothing in this migration does, so this is safe.
ALTER TYPE webhook_source_enum ADD VALUE 'click';
