-- Products can now be listed without a fixed price ("narxi so'rov
-- asosida" / price on request) — see entity.Product.PriceCents's doc
-- comment. The `price_cents >= 0` CHECK from 000011 still applies
-- (Postgres CHECK constraints pass NULL through unevaluated, so it only
-- ever rejects a negative price, never a missing one).
ALTER TABLE products ALTER COLUMN price_cents DROP NOT NULL;
