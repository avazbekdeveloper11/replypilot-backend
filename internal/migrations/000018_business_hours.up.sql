-- Business-hours AI gating: an org can restrict automated AI replies to a
-- daily time window (in the org's own timezone, organizations.timezone,
-- already present since migration 000001 — no new timezone column
-- needed). Outside that window, an inbound message is handed to a human
-- instead of getting an AI reply — see internal/usecase/ai's
-- HandleInboundMessage, which now checks these columns right after its
-- existing ai_active gate, using the same handoff() path already used
-- for "nothing usable to reply from".
--
-- business_hours_start/end are smallint minutes-since-midnight (0-1439),
-- not Postgres's `time` type — deliberately: this repo's Postgres driver
-- (gorm.io/driver/postgres over jackc/pgx) has no established convention
-- anywhere in this codebase for scanning a bare `time` column into a Go
-- field, and pgx's default type mapping for `time` is its own pgtype.Time,
-- not a plain string/int — a real scanning-fragility risk not worth
-- taking for two integers. NULL when business_hours_enabled is false,
-- meaning "not configured yet". "HH:MM" formatting happens only at the
-- HTTP DTO boundary (see internal/delivery/http/v1/dto.go).
--
-- Applied in the org's own timezone (organizations.timezone, already
-- present since migration 000001 — no new timezone column needed). See
-- internal/usecase/ai's HandleInboundMessage, which checks these columns
-- right after its existing ai_active gate, using the same handoff() path
-- already used for "nothing usable to reply from".
--
-- Deliberately added as columns on organizations directly, not a
-- separate settings table: this mirrors how `timezone` itself already
-- lives directly on organizations, and unlike comment_automation_settings
-- (migration 000017) there's no natural "does this row exist yet"
-- question here — every org either has hours configured or doesn't, a
-- boolean captures that, a whole extra table would not.
ALTER TABLE organizations
    ADD COLUMN business_hours_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN business_hours_start_minutes smallint,
    ADD COLUMN business_hours_end_minutes   smallint,
    ADD CONSTRAINT chk_business_hours_start_range
        CHECK (business_hours_start_minutes IS NULL OR business_hours_start_minutes BETWEEN 0 AND 1439),
    ADD CONSTRAINT chk_business_hours_end_range
        CHECK (business_hours_end_minutes IS NULL OR business_hours_end_minutes BETWEEN 0 AND 1439);
