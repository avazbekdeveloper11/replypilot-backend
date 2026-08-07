package ai

import (
	"time"

	"github.com/replypilot/backend/internal/domain/entity"
)

// withinBusinessHours reports whether now falls inside org's configured
// business-hours window, evaluated in the org's own timezone
// (entity.Organization.Timezone). Returns true (i.e. "don't gate, let
// the AI reply") whenever gating doesn't apply — hours disabled, not
// fully configured, or the org's timezone string fails to parse. Failing
// open here is deliberate, the same fail-safe posture as this package's
// other best-effort paths (see handleSendFailure, captureLeadIfPresent):
// a misconfigured or stale timezone string must never silently stop the
// AI from ever replying, since there's no user-facing error surface for
// that failure mode to be noticed and fixed quickly.
func withinBusinessHours(org *entity.Organization, now time.Time) bool {
	if org == nil || !org.BusinessHoursEnabled ||
		org.BusinessHoursStartMinutes == nil || org.BusinessHoursEndMinutes == nil {
		return true
	}

	loc, err := time.LoadLocation(org.Timezone)
	if err != nil {
		return true
	}

	local := now.In(loc)
	nowMinutes := local.Hour()*60 + local.Minute()

	start := *org.BusinessHoursStartMinutes
	end := *org.BusinessHoursEndMinutes
	switch {
	case start == end:
		// Degenerate config (both endpoints equal, e.g. never actually
		// set) — treat as "always open" rather than "always closed",
		// since always-closed would silently stop the AI from ever
		// replying with no clear signal anything is wrong.
		return true
	case start < end:
		// Normal same-day window, e.g. 09:00-18:00.
		return nowMinutes >= start && nowMinutes < end
	default:
		// Overnight window, e.g. 22:00-06:00 — wraps past midnight.
		return nowMinutes >= start || nowMinutes < end
	}
}
