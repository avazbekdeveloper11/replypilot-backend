-- Migration 000003 — webhook read policy (UP)
--
-- Closes the RLS gap flagged in instagram_account_repository.go: the webhook
-- receiver must resolve "which organization owns this Instagram account"
-- from Meta's payload BEFORE any tenant context exists — there is no
-- org_id to scope the tenant_isolation policy by yet, that's exactly what
-- the lookup produces. Under the tenant_isolation policy alone the lookup
-- returns zero rows.
--
-- Fix: an additional PERMISSIVE SELECT policy on instagram_accounts that
-- allows a read ONLY when the session has explicitly opted into a webhook
-- lookup by setting the GUC app.webhook_lookup = 'on'. RLS policies are
-- OR-combined, so this ADDS a narrowly-scoped allowed path without weakening
-- tenant_isolation for any normal query.
--
-- The application sets `SET LOCAL app.webhook_lookup = 'on'` (transaction-
-- scoped, auto-cleared on commit) around exactly one query —
-- InstagramAccountRepository.FindByIGUserID — and nowhere else. Because
-- SET LOCAL is transaction-scoped, the elevated read cannot leak to any
-- other query on the pooled connection.
--
-- Scope note: this policy permits reading ANY instagram_accounts row when
-- the flag is set. That is acceptable because (a) only the webhook-ingest
-- code path ever sets the flag, and (b) that path legitimately needs to
-- find an account across all tenants to route the inbound message. It is
-- deliberately SELECT-only — it grants no write bypass.

CREATE POLICY webhook_account_lookup ON instagram_accounts
    FOR SELECT
    USING (current_setting('app.webhook_lookup', true) = 'on');
