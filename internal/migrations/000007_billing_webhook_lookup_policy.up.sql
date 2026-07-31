-- Migration 000007 — billing webhook read policy (UP)
--
-- Same shape as 000003_webhook_read_policy, same reason: the Stripe
-- webhook receiver must resolve "which local subscription row does this
-- customer.subscription.updated/deleted event belong to" from Stripe's
-- own subscription id, BEFORE any tenant context exists — that's exactly
-- what the lookup produces. Under tenant_isolation alone it returns zero
-- rows. checkout.session.completed does NOT need this: the organization_id
-- is already known from the Checkout Session's metadata (set at session
-- creation, see usecase/billing.UseCase.CreateCheckoutSession), so that
-- write goes through the normal withTenant(orgID) path.
--
-- The application sets `SET LOCAL app.webhook_lookup = 'on'` around exactly
-- one query — SubscriptionRepository.FindByStripeSubscriptionID — mirroring
-- InstagramAccountRepository.FindByIGUserID exactly. SELECT-only, no write
-- bypass; see 000003's comment for the full RLS reasoning (OR-combined
-- PERMISSIVE policies, transaction-scoped GUC).

CREATE POLICY webhook_subscription_lookup ON subscriptions
    FOR SELECT
    USING (current_setting('app.webhook_lookup', true) = 'on');
