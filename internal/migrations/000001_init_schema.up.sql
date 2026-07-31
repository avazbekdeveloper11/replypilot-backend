-- Migration 000001 — init schema (UP)
-- Baseline schema for ReplyPilot. This is a "squashed" initial migration:
-- the entire current schema as one baseline. Every future change gets its
-- own incremental NNNNNN migration on top of this — never edit this file
-- after it has run anywhere.
-- Source of truth for structure. Reference data (permissions/roles/plans)
-- is a SEPARATE migration (000002) so schema and seed data version apart.

-- =============================================================================
-- ReplyPilot — Production PostgreSQL Schema
-- Target: PostgreSQL 15+, pgvector >= 0.5.0 (HNSW support)
-- =============================================================================
--
-- CONVENTIONS
--   - Primary keys: uuid, gen_random_uuid() (built into core since PG13, no
--     pgcrypto dependency for this specifically — pgcrypto kept for other
--     crypto helpers if you need them later).
--   - Multi-tenancy: shared schema, every tenant-scoped table carries
--     organization_id NOT NULL, enforced twice — app-layer filtering AND
--     Postgres Row-Level Security (see SECTION 12). Do not rely on one alone.
--   - Soft delete: deleted_at timestamptz, nullable, on every business entity
--     table. Uniqueness constraints are partial indexes with
--     "WHERE deleted_at IS NULL" so a soft-deleted row doesn't block reuse
--     of a name/slug/email. Append-only log tables (messages, webhook_logs,
--     audit_logs, ai_token_usage, usage_records, join tables) skip soft
--     delete — they're immutable by design, deleting them is an archival
--     operation (partition drop), not a row-level concern.
--   - Audit columns: created_at / updated_at on every mutable table
--     (updated_at maintained by trigger, not the application, so it can't
--     be forgotten in some code path). created_by / updated_by on tables
--     where "who did this" matters for support/compliance — not blindly
--     applied to every table, e.g. messages already captures sender via
--     sender_type/sender_user_id so a redundant created_by would be noise.
--
-- KNOWN TRADE-OFF — READ BEFORE MODIFYING messages / webhook_logs / audit_logs
--   These three tables are partitioned by RANGE on their timestamp column
--   (high volume, need cheap retention via DROP PARTITION). Postgres requires
--   a partitioned table's unique/PK constraints to include the partition key,
--   so `messages` has PRIMARY KEY (id, created_at) instead of just (id).
--   That breaks a plain `FOREIGN KEY (message_id) REFERENCES messages(id)`
--   from ai_responses. Fix used here: ai_responses stores both message_id
--   AND message_created_at, and the FK is a composite
--   FOREIGN KEY (message_id, message_created_at) REFERENCES messages(id, created_at).
--   This is the standard, correct pattern for FK-into-partitioned-table in
--   Postgres — not a workaround to be "fixed" later.
--
-- =============================================================================


-- =============================================================================
-- SECTION 1: EXTENSIONS
-- =============================================================================

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- crypto helper functions (token hashing, etc.)
CREATE EXTENSION IF NOT EXISTS citext;     -- case-insensitive text, used for email columns
CREATE EXTENSION IF NOT EXISTS vector;     -- pgvector, for knowledge_base_chunks.embedding


-- =============================================================================
-- SECTION 2: ENUM TYPES
-- =============================================================================

CREATE TYPE organization_status_enum AS ENUM ('trial', 'active', 'suspended', 'cancelled');
CREATE TYPE user_status_enum AS ENUM ('active', 'invited', 'suspended', 'deactivated');
CREATE TYPE team_member_status_enum AS ENUM ('invited', 'active', 'suspended', 'removed');
CREATE TYPE instagram_account_status_enum AS ENUM ('connected', 'expired', 'revoked', 'error');
CREATE TYPE conversation_status_enum AS ENUM ('ai_active', 'pending_human', 'human_active', 'resolved', 'closed');
CREATE TYPE message_direction_enum AS ENUM ('inbound', 'outbound');
CREATE TYPE message_sender_type_enum AS ENUM ('customer', 'ai', 'human', 'system');
CREATE TYPE message_type_enum AS ENUM ('text', 'image', 'video', 'audio', 'file', 'quick_reply', 'story_reply', 'story_mention', 'unsupported');
CREATE TYPE kb_source_type_enum AS ENUM ('file', 'url', 'manual_text', 'faq');
CREATE TYPE kb_document_status_enum AS ENUM ('pending', 'processing', 'ready', 'failed');
CREATE TYPE lead_status_enum AS ENUM ('new', 'qualified', 'contacted', 'converted', 'lost', 'disqualified');
CREATE TYPE subscription_status_enum AS ENUM ('trialing', 'active', 'past_due', 'canceled', 'unpaid', 'paused');
CREATE TYPE invoice_status_enum AS ENUM ('draft', 'open', 'paid', 'void', 'uncollectible');
CREATE TYPE usage_metric_type_enum AS ENUM ('messages', 'ai_responses', 'tokens', 'seats', 'kb_documents');
CREATE TYPE webhook_source_enum AS ENUM ('meta', 'stripe', 'telegram');
CREATE TYPE webhook_status_enum AS ENUM ('received', 'processing', 'processed', 'failed', 'ignored');
CREATE TYPE notification_channel_type_enum AS ENUM ('telegram', 'email', 'slack');


-- =============================================================================
-- SECTION 3: SHARED TRIGGER FUNCTION (updated_at)
-- =============================================================================

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;


-- =============================================================================
-- SECTION 4: USERS (global identity — an email logs in once, joins orgs via
-- team_members, not one-row-per-org)
-- =============================================================================

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           citext NOT NULL,
    password_hash   text,                      -- nullable: OAuth-only users have no local password
    full_name       text NOT NULL,
    avatar_url      text,
    status          user_status_enum NOT NULL DEFAULT 'invited',
    last_login_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE UNIQUE INDEX uq_users_email ON users(email) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_status ON users(status) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 5: ORGANIZATIONS (tenants)
-- =============================================================================

CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL,
    status      organization_status_enum NOT NULL DEFAULT 'trial',
    timezone    text NOT NULL DEFAULT 'UTC',
    settings    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by  uuid REFERENCES users(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX uq_organizations_slug ON organizations(slug) WHERE deleted_at IS NULL;
CREATE INDEX idx_organizations_status ON organizations(status) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_organizations_updated_at
    BEFORE UPDATE ON organizations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 6: PERMISSIONS, ROLES, ROLE_PERMISSIONS, TEAM_MEMBERS
-- =============================================================================

CREATE TABLE permissions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key         text NOT NULL,           -- e.g. 'conversations.write'
    category    text NOT NULL,           -- e.g. 'conversations'
    description text,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_permissions_key ON permissions(key);

-- roles.organization_id IS NULL => system role, shared/read-only across all
-- tenants (Owner, Admin, Agent, Viewer). NOT NULL => tenant-defined custom role.
CREATE TABLE roles (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text,
    is_system       boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

-- NULLs don't collide in a standard unique index, so system roles (org_id
-- NULL) need their own partial index or two rows named "Owner" with NULL
-- org_id would both be allowed. Split into two explicit constraints:
CREATE UNIQUE INDEX uq_roles_org_name ON roles(organization_id, name)
    WHERE organization_id IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_roles_system_name ON roles(name)
    WHERE organization_id IS NULL AND deleted_at IS NULL;

CREATE TRIGGER trg_roles_updated_at
    BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE role_permissions (
    role_id       uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE team_members (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id         uuid NOT NULL REFERENCES roles(id) ON DELETE RESTRICT, -- can't delete a role still in use
    status          team_member_status_enum NOT NULL DEFAULT 'invited',
    invited_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    invited_at      timestamptz NOT NULL DEFAULT now(),
    joined_at       timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE UNIQUE INDEX uq_team_members_org_user ON team_members(organization_id, user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_team_members_role ON team_members(role_id);
CREATE INDEX idx_team_members_org_status ON team_members(organization_id, status) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_team_members_updated_at
    BEFORE UPDATE ON team_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 7: INSTAGRAM ACCOUNTS
-- =============================================================================

CREATE TABLE instagram_accounts (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    ig_user_id              text NOT NULL,             -- Meta-side account id
    username                text,
    access_token_encrypted  bytea NOT NULL,             -- envelope-encrypted at app layer, never plaintext
    token_expires_at        timestamptz,                -- long-lived token, ~60 days, refreshed proactively
    status                  instagram_account_status_enum NOT NULL DEFAULT 'connected',
    webhook_subscribed      boolean NOT NULL DEFAULT false,
    connected_by_user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz
);

CREATE UNIQUE INDEX uq_instagram_accounts_ig_user_id ON instagram_accounts(ig_user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_instagram_accounts_org ON instagram_accounts(organization_id) WHERE deleted_at IS NULL;
-- Feeds the daily token-refresh job: accounts whose token is about to expire.
CREATE INDEX idx_instagram_accounts_token_expiry ON instagram_accounts(token_expires_at)
    WHERE deleted_at IS NULL AND status = 'connected';

CREATE TRIGGER trg_instagram_accounts_updated_at
    BEFORE UPDATE ON instagram_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 8: TAGS
-- =============================================================================

CREATE TABLE tags (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    color           text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE UNIQUE INDEX uq_tags_org_name ON tags(organization_id, name) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_tags_updated_at
    BEFORE UPDATE ON tags
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 9: CONVERSATIONS + CONVERSATION_TAGS + LEADS
-- =============================================================================

CREATE TABLE conversations (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    instagram_account_id    uuid NOT NULL REFERENCES instagram_accounts(id) ON DELETE CASCADE,
    customer_ig_id          text NOT NULL,           -- Meta's scoped id for the customer
    customer_username       text,
    status                  conversation_status_enum NOT NULL DEFAULT 'ai_active',
    assigned_user_id        uuid REFERENCES users(id) ON DELETE SET NULL,
    last_message_at         timestamptz,
    last_message_preview    text,
    unread_count            integer NOT NULL DEFAULT 0 CHECK (unread_count >= 0),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz
);

-- One conversation thread per customer per connected account.
CREATE UNIQUE INDEX uq_conversations_account_customer
    ON conversations(instagram_account_id, customer_ig_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_conversations_org_status ON conversations(organization_id, status) WHERE deleted_at IS NULL;
CREATE INDEX idx_conversations_assigned ON conversations(assigned_user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_conversations_org_last_message
    ON conversations(organization_id, last_message_at DESC) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_conversations_updated_at
    BEFORE UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE conversation_tags (
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    tag_id          uuid NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, -- denormalized for RLS + indexing
    tagged_by       uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, tag_id)
);

CREATE INDEX idx_conversation_tags_tag ON conversation_tags(tag_id);

CREATE TABLE leads (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    conversation_id         uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    full_name               text,
    email                   citext,
    phone                   text,
    company                 text,
    status                  lead_status_enum NOT NULL DEFAULT 'new',
    estimated_value_cents   integer CHECK (estimated_value_cents >= 0),
    source                  text,
    custom_fields           jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz,
    created_by              uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by              uuid REFERENCES users(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX uq_leads_conversation ON leads(conversation_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_leads_org_status ON leads(organization_id, status) WHERE deleted_at IS NULL;
CREATE INDEX idx_leads_org_email ON leads(organization_id, email) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_leads_updated_at
    BEFORE UPDATE ON leads
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 10: MESSAGES (partitioned monthly — see trade-off note at top)
-- =============================================================================

CREATE TABLE messages (
    id              uuid NOT NULL DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    direction       message_direction_enum NOT NULL,
    sender_type     message_sender_type_enum NOT NULL,
    sender_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,  -- set only when sender_type = 'human'
    message_type    message_type_enum NOT NULL DEFAULT 'text',
    content         text,
    attachment_url  text,
    ig_message_id   text,                                          -- Meta's message id, for idempotent ingestion
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,                                   -- moderation/removal only, not a normal edit path
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_messages_conversation_created ON messages(conversation_id, created_at DESC);
CREATE INDEX idx_messages_org_created ON messages(organization_id, created_at DESC);
-- Composite because partitioned uniqueness must include the partition key.
-- Primary line of defense against duplicate delivery is the Redis idempotency
-- key at ingestion (see architecture doc §6); this is the DB-level backstop.
CREATE UNIQUE INDEX uq_messages_ig_message_id ON messages(ig_message_id, created_at)
    WHERE ig_message_id IS NOT NULL;
CREATE INDEX idx_messages_content_fts ON messages USING gin (to_tsvector('english', coalesce(content, '')));

-- Monthly partitions. Automate creation going forward with pg_partman or a
-- scheduled job — do not hand-roll this in application code.
CREATE TABLE messages_2026_07 PARTITION OF messages FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE messages_2026_08 PARTITION OF messages FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE messages_2026_09 PARTITION OF messages FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
CREATE TABLE messages_default PARTITION OF messages DEFAULT;  -- safety net, not a substitute for real partitions


-- =============================================================================
-- SECTION 11: KNOWLEDGE BASE (documents + pgvector chunks)
-- =============================================================================

CREATE TABLE knowledge_base_documents (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title           text NOT NULL,
    source_type     kb_source_type_enum NOT NULL,
    file_url        text,
    status          kb_document_status_enum NOT NULL DEFAULT 'pending',
    error_message   text,
    uploaded_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE INDEX idx_kb_documents_org_status ON knowledge_base_documents(organization_id, status) WHERE deleted_at IS NULL;

CREATE TRIGGER trg_kb_documents_updated_at
    BEFORE UPDATE ON knowledge_base_documents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Dimension is fixed at 1536 (OpenAI text-embedding-3-small / ada-002).
-- If you switch embedding models to a different dimension, this column and
-- its index must be recreated — pgvector columns are dimension-fixed.
CREATE TABLE knowledge_base_chunks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    document_id     uuid NOT NULL REFERENCES knowledge_base_documents(id) ON DELETE CASCADE,
    chunk_index     integer NOT NULL,
    content         text NOT NULL,
    token_count     integer CHECK (token_count >= 0),
    embedding       vector(1536),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

CREATE UNIQUE INDEX uq_kb_chunks_document_index ON knowledge_base_chunks(document_id, chunk_index) WHERE deleted_at IS NULL;
CREATE INDEX idx_kb_chunks_org ON knowledge_base_chunks(organization_id) WHERE deleted_at IS NULL;
-- HNSW requires pgvector >= 0.5.0. Tune m / ef_construction once you have
-- real corpus size and recall/latency numbers — defaults are a reasonable start.
CREATE INDEX idx_kb_chunks_embedding_hnsw ON knowledge_base_chunks
    USING hnsw (embedding vector_cosine_ops);

CREATE TRIGGER trg_kb_chunks_updated_at
    BEFORE UPDATE ON knowledge_base_chunks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 12: AI RESPONSES, CITATIONS, TOKEN USAGE
-- =============================================================================

CREATE TABLE ai_responses (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    conversation_id         uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id              uuid NOT NULL,
    message_created_at      timestamptz NOT NULL,
    model_used              text NOT NULL,
    prompt_tokens           integer NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens       integer NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    total_tokens            integer GENERATED ALWAYS AS (prompt_tokens + completion_tokens) STORED,
    confidence_score        numeric(5,4) CHECK (confidence_score >= 0 AND confidence_score <= 1),
    was_handoff_triggered   boolean NOT NULL DEFAULT false,
    latency_ms              integer CHECK (latency_ms >= 0),
    created_at              timestamptz NOT NULL DEFAULT now(),
    -- composite FK into the partitioned messages table — see trade-off note at top
    FOREIGN KEY (message_id, message_created_at) REFERENCES messages(id, created_at) ON DELETE CASCADE
);

CREATE UNIQUE INDEX uq_ai_responses_message ON ai_responses(message_id);
CREATE INDEX idx_ai_responses_conversation ON ai_responses(conversation_id);
CREATE INDEX idx_ai_responses_org_created ON ai_responses(organization_id, created_at DESC);

-- Audit trail: exactly which KB chunks grounded a given AI answer.
CREATE TABLE ai_response_citations (
    ai_response_id    uuid NOT NULL REFERENCES ai_responses(id) ON DELETE CASCADE,
    kb_chunk_id       uuid NOT NULL REFERENCES knowledge_base_chunks(id) ON DELETE CASCADE,
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    similarity_score  numeric(5,4) CHECK (similarity_score >= 0 AND similarity_score <= 1),
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (ai_response_id, kb_chunk_id)
);

CREATE INDEX idx_ai_response_citations_chunk ON ai_response_citations(kb_chunk_id);

-- Separate from ai_responses so token/cost accounting isn't coupled to
-- conversational data — this is what usage rollups and billing read from.
CREATE TABLE ai_token_usage (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    ai_response_id      uuid REFERENCES ai_responses(id) ON DELETE SET NULL,
    provider            text NOT NULL,     -- e.g. 'openai', 'anthropic'
    model               text NOT NULL,
    prompt_tokens       integer NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens   integer NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    total_tokens        integer GENERATED ALWAYS AS (prompt_tokens + completion_tokens) STORED,
    cost_usd            numeric(10,6) NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_ai_token_usage_org_created ON ai_token_usage(organization_id, created_at DESC);


-- =============================================================================
-- SECTION 13: PLANS, SUBSCRIPTIONS, INVOICES, USAGE
-- =============================================================================

CREATE TABLE plans (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code                   text NOT NULL,        -- e.g. 'starter', 'pro', 'enterprise'
    name                   text NOT NULL,
    price_monthly_cents    integer NOT NULL CHECK (price_monthly_cents >= 0),
    price_yearly_cents     integer NOT NULL CHECK (price_yearly_cents >= 0),
    message_limit          integer CHECK (message_limit >= 0),   -- NULL = unlimited
    seat_limit             integer CHECK (seat_limit >= 0),      -- NULL = unlimited
    features               jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_active              boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_plans_code ON plans(code);

CREATE TRIGGER trg_plans_updated_at
    BEFORE UPDATE ON plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE subscriptions (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    plan_id                 uuid NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
    stripe_subscription_id  text,
    stripe_customer_id      text,
    status                  subscription_status_enum NOT NULL DEFAULT 'trialing',
    current_period_start    timestamptz,
    current_period_end      timestamptz,
    cancel_at_period_end    boolean NOT NULL DEFAULT false,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz
);

CREATE UNIQUE INDEX uq_subscriptions_stripe_id ON subscriptions(stripe_subscription_id)
    WHERE stripe_subscription_id IS NOT NULL;
-- At most one "live" subscription per org; history (canceled ones) can pile up.
CREATE UNIQUE INDEX uq_subscriptions_org_active ON subscriptions(organization_id)
    WHERE status IN ('trialing', 'active', 'past_due', 'paused') AND deleted_at IS NULL;
CREATE INDEX idx_subscriptions_org ON subscriptions(organization_id);

CREATE TRIGGER trg_subscriptions_updated_at
    BEFORE UPDATE ON subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE invoices (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    subscription_id     uuid REFERENCES subscriptions(id) ON DELETE SET NULL,
    stripe_invoice_id   text,
    amount_due_cents    integer NOT NULL CHECK (amount_due_cents >= 0),
    amount_paid_cents   integer NOT NULL DEFAULT 0 CHECK (amount_paid_cents >= 0),
    currency            char(3) NOT NULL DEFAULT 'USD',
    status              invoice_status_enum NOT NULL DEFAULT 'draft',
    issued_at           timestamptz,
    paid_at             timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_invoices_stripe_id ON invoices(stripe_invoice_id) WHERE stripe_invoice_id IS NOT NULL;
CREATE INDEX idx_invoices_org_status ON invoices(organization_id, status);

CREATE TRIGGER trg_invoices_updated_at
    BEFORE UPDATE ON invoices
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE usage_records (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    metric_type       usage_metric_type_enum NOT NULL,
    quantity          bigint NOT NULL CHECK (quantity >= 0),
    period_start      timestamptz NOT NULL,
    period_end        timestamptz NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (period_end > period_start)
);

CREATE INDEX idx_usage_records_org_metric_period ON usage_records(organization_id, metric_type, period_start DESC);


-- =============================================================================
-- SECTION 14: WEBHOOK LOGS (partitioned monthly)
-- =============================================================================

CREATE TABLE webhook_logs (
    id                uuid NOT NULL DEFAULT gen_random_uuid(),
    organization_id   uuid REFERENCES organizations(id) ON DELETE SET NULL,  -- nullable: may be unresolved pre-parse
    source            webhook_source_enum NOT NULL,
    event_type        text,
    payload           jsonb NOT NULL,
    signature_valid   boolean NOT NULL,
    status            webhook_status_enum NOT NULL DEFAULT 'received',
    error_message     text,
    received_at       timestamptz NOT NULL DEFAULT now(),
    processed_at      timestamptz,
    PRIMARY KEY (id, received_at)
) PARTITION BY RANGE (received_at);

CREATE INDEX idx_webhook_logs_org ON webhook_logs(organization_id, received_at DESC);
CREATE INDEX idx_webhook_logs_status ON webhook_logs(status) WHERE status IN ('received', 'processing', 'failed');

CREATE TABLE webhook_logs_2026_07 PARTITION OF webhook_logs FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE webhook_logs_2026_08 PARTITION OF webhook_logs FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE webhook_logs_2026_09 PARTITION OF webhook_logs FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
CREATE TABLE webhook_logs_default PARTITION OF webhook_logs DEFAULT;


-- =============================================================================
-- SECTION 15: AUDIT LOGS (partitioned monthly, append-only)
-- =============================================================================

CREATE TABLE audit_logs (
    id               uuid NOT NULL DEFAULT gen_random_uuid(),
    organization_id  uuid REFERENCES organizations(id) ON DELETE SET NULL,
    actor_user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    action           text NOT NULL,          -- e.g. 'billing.plan_changed', 'kb.document_deleted'
    entity_type      text NOT NULL,
    entity_id        uuid,
    metadata         jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip_address       inet,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_audit_logs_org_created ON audit_logs(organization_id, created_at DESC);
CREATE INDEX idx_audit_logs_entity ON audit_logs(entity_type, entity_id);

CREATE TABLE audit_logs_2026_07 PARTITION OF audit_logs FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
CREATE TABLE audit_logs_2026_08 PARTITION OF audit_logs FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE audit_logs_2026_09 PARTITION OF audit_logs FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
CREATE TABLE audit_logs_default PARTITION OF audit_logs DEFAULT;


-- =============================================================================
-- SECTION 16: NOTIFICATION CHANNELS (Telegram etc. — bonus, referenced by
-- the product's "Telegram Notifications" feature, not explicitly requested
-- in this schema but has no home without it)
-- =============================================================================

CREATE TABLE notification_channels (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    channel_type        notification_channel_type_enum NOT NULL,
    config              jsonb NOT NULL DEFAULT '{}'::jsonb,   -- e.g. {"chat_id": "..."} for telegram
    notify_on_handoff   boolean NOT NULL DEFAULT true,
    notify_on_billing   boolean NOT NULL DEFAULT true,
    is_active           boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz
);

CREATE UNIQUE INDEX uq_notification_channels_org_type ON notification_channels(organization_id, channel_type)
    WHERE deleted_at IS NULL;

CREATE TRIGGER trg_notification_channels_updated_at
    BEFORE UPDATE ON notification_channels
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();


-- =============================================================================
-- SECTION 17: ROW-LEVEL SECURITY
-- =============================================================================
-- Pattern: every tenant-scoped table gets RLS enabled + a policy comparing
-- organization_id to a session variable the application sets at the start of
-- each request's transaction: SET app.current_org_id = '<uuid>'.
--
-- This is the backstop, not the primary control — the API layer must still
-- filter by tenant explicitly. RLS exists so an application bug (a missing
-- WHERE clause) fails closed instead of leaking another tenant's DMs.
--
-- Two DB roles, not one:
--   replypilot_app       — used by the API/workers, subject to RLS
--   replypilot_migrator  — BYPASSRLS, used only for migrations/admin tooling
-- =============================================================================

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'instagram_accounts', 'conversations', 'messages',
        'knowledge_base_documents', 'knowledge_base_chunks',
        'ai_responses', 'ai_response_citations', 'ai_token_usage',
        'tags', 'conversation_tags', 'leads', 'team_members',
        'subscriptions', 'invoices', 'usage_records', 'notification_channels'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (organization_id = current_setting(''app.current_org_id'', true)::uuid);',
            t
        );
    END LOOP;
END $$;

-- roles is a special case: system roles (organization_id IS NULL) must be
-- visible to every tenant, not just one.
ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON roles
    USING (organization_id IS NULL OR organization_id = current_setting('app.current_org_id', true)::uuid);

-- webhook_logs and audit_logs intentionally excluded from the loop: they can
-- have organization_id = NULL (unresolved/system events) and are primarily
-- queried by internal ops tooling running as replypilot_migrator (BYPASSRLS),
-- not by tenant-facing application code.

