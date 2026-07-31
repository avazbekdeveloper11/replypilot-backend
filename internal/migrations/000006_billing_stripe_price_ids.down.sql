-- Migration 000006 — billing: Stripe price ids on plans (DOWN)

ALTER TABLE plans DROP COLUMN IF EXISTS stripe_price_id_monthly;
ALTER TABLE plans DROP COLUMN IF EXISTS stripe_price_id_yearly;
