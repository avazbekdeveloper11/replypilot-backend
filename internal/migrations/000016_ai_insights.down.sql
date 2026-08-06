DROP TABLE IF EXISTS ai_insights_cache;

ALTER TABLE conversations
    DROP COLUMN IF EXISTS ai_summary,
    DROP COLUMN IF EXISTS ai_summary_generated_at;
