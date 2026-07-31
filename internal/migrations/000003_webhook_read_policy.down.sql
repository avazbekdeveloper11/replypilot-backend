-- Migration 000003 — webhook read policy (DOWN)
DROP POLICY IF EXISTS webhook_account_lookup ON instagram_accounts;
