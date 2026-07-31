package repository

import (
	"context"

	"github.com/replypilot/backend/internal/domain/entity"
)

// OrganizationSummary is one row of the admin panel's organization list —
// the Organization itself plus the cross-tenant aggregates an admin needs
// at a glance (active member count, current plan/subscription status).
// MemberCount counts only active team_members rows, same "active" filter
// TeamMemberRepository's own listing uses. PlanCode/SubscriptionStatus
// are nil for an organization with no subscription row at all (never
// completed Checkout) — see entity.Subscription's doc comment.
type OrganizationSummary struct {
	Organization       *entity.Organization
	MemberCount        int64
	PlanCode           *string
	SubscriptionStatus *string
}

// PlanSubscriptionCount is one plan's count of active/trialing
// subscriptions — the admin panel's breakdown, not a single MRR figure
// alone, since MRRCentsApprox (see PlatformStats) can't tell monthly vs.
// yearly billing apart (Subscription doesn't persist which period was
// chosen — see that entity's doc comment) and so needs this breakdown to
// be sanity-checked against.
type PlanSubscriptionCount struct {
	PlanCode string
	PlanName string
	Count    int64
}

// PlatformStats is the admin panel's dashboard — platform-wide, not
// scoped to any one organization.
//
// MRRCentsApprox is a labeled approximation, not a real MRR figure: it
// sums each active/trialing subscription's plan at PriceMonthlyCents,
// but this codebase does not persist which billing period (monthly or
// yearly) a given subscription actually chose at Checkout — see
// entity.Subscription's doc comment. A subscriber who actually pays
// yearly is counted here at the monthly-equivalent price, which
// overstates true MRR for any such subscriber. Fixing this for real
// requires persisting billing_period on the subscriptions table (a
// migration + webhook handler change) and was out of scope here — this
// field exists because a labeled, directionally-useful approximation is
// more honest and more useful than omitting revenue from the admin panel
// entirely, not because it's precise.
type PlatformStats struct {
	TotalOrganizations  int64
	TotalUsers          int64
	TotalConversations  int64
	TotalMessages       int64
	ActiveSubscriptions int64
	MRRCentsApprox      int64
	SubscriptionsByPlan []PlanSubscriptionCount
}

// AdminRepository is the one repository in this codebase that
// deliberately reads across every tenant — every method here bypasses
// (or, for users/organizations, was never subject to) row-level
// security. See internal/repository/postgres/platform_admin.go's doc
// comment on withPlatformAdmin for the mechanism, and
// internal/usecase/admin for the authorization boundary (every usecase
// method here must only ever be reachable through the
// RequirePlatformAdmin middleware).
type AdminRepository interface {
	ListOrganizations(ctx context.Context) ([]OrganizationSummary, error)
	Stats(ctx context.Context) (*PlatformStats, error)
}
