-- Migration 000009 — token-refresh read policy (UP)
--
-- cmd/token-refresh (see that binary's doc comment) is a scheduled,
-- run-once-and-exit job that finds every InstagramAccount across ALL
-- organizations whose long-lived token is nearing its ~60-day expiry, so it
-- can call metaapi.Client.RefreshLongLivedToken before the token dies. Like
-- the webhook receiver (migration 000003) and the platform-admin panel
-- (migration 000008), this job has no single org_id to scope by — by
-- design it needs to see across every tenant in one query.
--
-- Fix: same GUC-gated PERMISSIVE-policy pattern as those two migrations —
-- an additional SELECT policy on instagram_accounts that only allows a read
-- when the session has set `app.token_refresh_lookup = 'on'`. RLS policies
-- are OR-combined, so this adds one narrowly-scoped allowed path without
-- weakening tenant_isolation for any normal query.
--
-- The application sets this GUC with `SET LOCAL` (transaction-scoped, auto-
-- cleared on commit) around exactly one query —
-- InstagramAccountRepository.ListNearingExpiry — and nowhere else. It is
-- deliberately SELECT-only: after the job fetches an account this way, it
-- writes the refreshed token back through the ordinary
-- InstagramAccountRepository.Update, which IS tenant-scoped (via
-- withTenant, keyed on the account's own OrganizationID) — no write bypass
-- exists or is needed here, since by the time we write, we know the org.

CREATE POLICY token_refresh_lookup ON instagram_accounts
    FOR SELECT
    USING (current_setting('app.token_refresh_lookup', true) = 'on');
