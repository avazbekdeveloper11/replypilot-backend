-- Reverting requires every existing NULL to become a concrete value first,
-- or the NOT NULL would fail to re-apply against real data — 0 is the same
-- "no price" placeholder the pre-migration schema never actually allowed,
-- but it's the least surprising fallback for a down-migration nobody
-- expects to run against production data anyway.
UPDATE products SET price_cents = 0 WHERE price_cents IS NULL;
ALTER TABLE products ALTER COLUMN price_cents SET NOT NULL;
