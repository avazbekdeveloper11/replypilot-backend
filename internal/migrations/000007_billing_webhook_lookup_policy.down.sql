-- Migration 000007 — billing webhook read policy (DOWN)

DROP POLICY IF EXISTS webhook_subscription_lookup ON subscriptions;
