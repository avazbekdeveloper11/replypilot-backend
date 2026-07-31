-- Migration 000009 — token-refresh read policy (DOWN)

DROP POLICY IF EXISTS token_refresh_lookup ON instagram_accounts;
