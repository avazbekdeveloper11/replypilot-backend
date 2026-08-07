-- Comment-to-DM automation: when someone comments on one of the org's
-- Instagram posts, reply to them privately in DMs (Meta's "private reply"
-- mechanism) and let the normal AI pipeline take the conversation from
-- there. This is the single most-used feature of competing tools
-- (ManyChat's "comment X and I'll DM you" flows) and was the biggest
-- functional gap in this product.
--
-- Two Meta constraints drove this design, both documented at
-- https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/messaging-api#private-replies:
--   1. A private reply may only be sent ONCE per comment, ever. Re-sending
--      is a hard API error, not a silently ignored no-op — hence the
--      idempotency index on processed_comments below.
--   2. The first message must be addressed to the COMMENT id
--      (recipient.comment_id), not the commenter's user id. Only after
--      that first message does the thread exist and accept normal
--      recipient.id sends. That's why the ingested message carries the
--      comment id in its metadata — see internal/usecase/ai's
--      privateReplyCommentID lookup.

CREATE TABLE comment_automation_settings (
    organization_id   uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled           boolean NOT NULL DEFAULT false,
    -- When set, a public reply is also posted under the comment itself
    -- ("Javob berdik, DM'ni tekshiring 👇"). Optional and static, NOT
    -- AI-generated: a public reply is visible to everyone forever, so a
    -- fixed string the org wrote themselves is the safe default. Empty =>
    -- private reply only.
    public_reply_text text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_comment_automation_settings_updated_at
    BEFORE UPDATE ON comment_automation_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE comment_automation_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON comment_automation_settings
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);

-- Every comment this org has already auto-replied to. Exists purely for
-- constraint (1) above: Meta redelivers webhooks, and a duplicate private
-- reply is a hard error that would otherwise mark the whole delivery
-- failed. Insert-first-then-act (see instagram.WebhookUseCase.handleComment)
-- makes the unique index itself the concurrency guard, rather than a
-- check-then-act race between two redeliveries arriving at once.
-- No conversation_id column here on purpose: the claim is necessarily
-- written BEFORE the conversation is resolved (that ordering is the whole
-- point — see above), so such a column could only ever be NULL unless a
-- second write went back to fill it in. Nothing reads it, so that write
-- would be pure cost. The comment's resulting conversation is already
-- discoverable from messages.ig_message_id, which stores 'ig_comment:{id}'.
CREATE TABLE processed_comments (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    ig_comment_id     text NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_processed_comments_comment_id ON processed_comments(ig_comment_id);
CREATE INDEX idx_processed_comments_org ON processed_comments(organization_id);

ALTER TABLE processed_comments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON processed_comments
    USING (organization_id = current_setting('app.current_org_id', true)::uuid);

-- The webhook path resolves the org from instagram_accounts (which already
-- has its own webhook_account_lookup policy from migration 000003) and
-- then does every read/write here inside that resolved org's normal
-- tenant-scoped transaction — so neither table above needs a bypass
-- policy, same reasoning as the orders table in migration 000015.
