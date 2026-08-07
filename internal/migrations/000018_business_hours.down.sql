ALTER TABLE organizations
    DROP COLUMN IF EXISTS business_hours_end_minutes,
    DROP COLUMN IF EXISTS business_hours_start_minutes,
    DROP COLUMN IF EXISTS business_hours_enabled;
