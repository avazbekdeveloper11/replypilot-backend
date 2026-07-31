-- Migration 000006 — billing: Stripe price ids on plans (UP)
-- plans.code/name/price_*_cents already exist (see 000002's seed data) but
-- nothing ties a plan to an actual Stripe Price object — without these
-- columns, checkout-session creation has no price id to hand Stripe.
-- Nullable: 'enterprise' is custom-pricing (sales-assisted), never sold via
-- self-serve Checkout, so it legitimately has no Stripe price on either
-- cadence.

ALTER TABLE plans ADD COLUMN stripe_price_id_monthly text;
ALTER TABLE plans ADD COLUMN stripe_price_id_yearly text;

-- Filled in per-environment (Stripe price ids differ between test/live
-- mode and between Stripe accounts) — not seeded with real values here.
-- Set these by hand (or a small ops script) after creating the
-- corresponding Products/Prices in the Stripe dashboard:
--   UPDATE plans SET stripe_price_id_monthly = 'price_...', stripe_price_id_yearly = 'price_...' WHERE code = 'starter';
--   UPDATE plans SET stripe_price_id_monthly = 'price_...', stripe_price_id_yearly = 'price_...' WHERE code = 'pro';
