-- Migration 000004 — team_members email-lookup read policy (DOWN)
DROP POLICY IF EXISTS member_email_lookup ON team_members;
