-- Migration 000010 — platform settings (UP)
--
-- A tiny key/value store for ReplyPilot-staff-configured, platform-wide
-- secrets — starting with the Gemini API key (see internal/usecase/ai and
-- internal/usecase/knowledgebase, both of which call Google's Gemini API
-- using a single key that belongs to ReplyPilot, not to any one tenant).
-- Previously that key only came from the GEMINI_API_KEY env var, meaning
-- rotating it required editing .env and redeploying every service that
-- reads config. This table lets a platform admin set/rotate it from the
-- admin panel instead — see internal/usecase/platformsettings.
--
-- value_encrypted uses the same AES-256-GCM envelope
-- (pkg/crypto.AESGCMEncryptor, keyed by TOKEN_ENCRYPTION_KEY) already used
-- for Instagram access tokens — one encryption-at-rest mechanism for every
-- secret this codebase stores in Postgres, not a second one invented here.
--
-- No RLS: like `plans` (see model.go's PlanModel doc comment), this is
-- global platform data, not tenant data — there is no organization_id to
-- scope by. Access control is entirely at the HTTP layer
-- (RequirePlatformAdmin on every /v1/admin/* route).

CREATE TABLE platform_settings (
    key             text PRIMARY KEY,
    value_encrypted bytea NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    updated_by      uuid REFERENCES users(id) ON DELETE SET NULL
);
