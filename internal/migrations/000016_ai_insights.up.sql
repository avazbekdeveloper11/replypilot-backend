-- AI-generated analysis, in two shapes: per-conversation ("what did this
-- customer actually talk about") and org-wide ("how many sales did we make
-- through the AI, what do customers seem to think of us"). Both are
-- deliberately on-demand (a button, not something that runs on every page
-- load or every inbound message) and cached until explicitly regenerated —
-- see internal/usecase/conversation.UseCase.Summarize and
-- internal/usecase/insights for why.

ALTER TABLE conversations
    ADD COLUMN ai_summary              text,
    ADD COLUMN ai_summary_generated_at timestamptz;

-- One row per organization, upserted in place on every regenerate — same
-- "at most one, no history kept" shape as click_integrations, not an
-- append-only log. Regenerating simply overwrites; the previous summary
-- isn't worth keeping around once a newer one exists.
CREATE TABLE ai_insights_cache (
    organization_id    uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    summary             text NOT NULL,
    sales_count         integer NOT NULL,
    sales_amount_cents  bigint NOT NULL,
    lead_count          integer NOT NULL,
    conversation_count  integer NOT NULL,
    generated_at        timestamptz NOT NULL
);

ALTER TABLE ai_insights_cache ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_insights_cache
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);
