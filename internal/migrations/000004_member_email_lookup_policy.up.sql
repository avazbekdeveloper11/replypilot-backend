-- Migration 000004 — team_members email-lookup read policy (UP)
--
-- Closes the same category of RLS gap as migration 000003, this time for
-- login-discovery: given an email, the login flow needs to answer "which
-- organizations does this user belong to" BEFORE any org has been chosen
-- — there is no org_id yet to scope the tenant_isolation policy by, that
-- is exactly what this lookup produces. Under tenant_isolation alone the
-- query returns zero rows for every user.
--
-- Fix: an additional PERMISSIVE SELECT policy on team_members that allows
-- a read ONLY when the session has explicitly opted in via the GUC
-- app.member_lookup = 'on'. RLS policies are OR-combined, so this ADDS a
-- narrowly-scoped allowed path without weakening tenant_isolation for any
-- normal (org-scoped) query.
--
-- The application sets `SET LOCAL app.member_lookup = 'on'` (transaction-
-- scoped, auto-cleared on commit) around exactly one query —
-- TeamMemberRepository.ListByUserID — and nowhere else. Because SET LOCAL
-- is transaction-scoped, the elevated read cannot leak to any other query
-- on the pooled connection.
--
-- Scope note: this policy permits reading ANY team_members row (across
-- all tenants) when the flag is set. Acceptable because (a) only the
-- login-discovery code path ever sets the flag, (b) it is SELECT-only —
-- no write bypass, and (c) the rows it exposes (organization_id, role_id,
-- status) are already what the login screen needs to show "which
-- workspace do you want to log into" — no password hash or other secret
-- is on this table.

CREATE POLICY member_email_lookup ON team_members
    FOR SELECT
    USING (current_setting('app.member_lookup', true) = 'on');
