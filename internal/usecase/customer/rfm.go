package customer

import (
	"sort"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

// assignRFMSegments scores every customer with at least one paid order on
// Recency and Frequency (1=worst .. 5=best, relative to this org's own
// customer distribution — quantile-based, not fixed thresholds, since "a
// big order" means something different to a shop selling $5 items vs $500
// items) and classifies them into one of five segments, mutating each
// summary in place. Customers with zero paid orders get RFMSegmentNew
// instead — there's no purchase history to score against.
//
// This mirrors the RFM methodology competitor platforms (e.g. Reveo)
// expose as a paid feature. The Monetary score is computed and exposed
// too (for sorting/display) but deliberately left out of the segment
// classification itself — Recency+Frequency alone already answers the
// question this feature exists for ("who should get a cashback nudge
// right now"), and folding in Monetary would only matter for shops with
// wildly different price tiers per customer.
func assignRFMSegments(summaries []*entity.CustomerSummary) {
	recencyInput := make(map[uuid.UUID]float64)
	frequencyInput := make(map[uuid.UUID]float64)
	monetaryInput := make(map[uuid.UUID]float64)

	for _, s := range summaries {
		if s.PaidOrderCount == 0 || s.LastPaidAt == nil {
			continue
		}
		// Later timestamp = more recent = better, same "higher is better"
		// direction as frequency/monetary — lets quantileScores treat all
		// three identically.
		recencyInput[s.ConversationID] = float64(s.LastPaidAt.Unix())
		frequencyInput[s.ConversationID] = float64(s.PaidOrderCount)
		monetaryInput[s.ConversationID] = float64(s.TotalPaidCents)
	}

	recencyScores := quantileScores(recencyInput)
	frequencyScores := quantileScores(frequencyInput)
	monetaryScores := quantileScores(monetaryInput)

	for _, s := range summaries {
		r, hasOrders := recencyScores[s.ConversationID]
		if !hasOrders {
			s.Segment = entity.RFMSegmentNew
			continue
		}
		f := frequencyScores[s.ConversationID]
		m := monetaryScores[s.ConversationID]
		s.RecencyScore = r
		s.FrequencyScore = f
		s.MonetaryScore = m
		s.Segment = classifySegment(r, f)
	}
}

// quantileScores buckets values into five equal-frequency groups
// (quintiles) and returns a 1-5 score per id, 5 being the highest values.
// Ties (e.g. most shops will have many customers with exactly 1 paid
// order) land in whichever bucket their sort position falls into — an
// approximation, not exact quantile math, which is fine at the
// customer-list sizes this feature operates at (capped at
// postgres.defaultCustomerListLimit).
//
// Scored with a ceiling division, score = ceil(rank*5/n) where rank is
// 1-indexed (1..n), not floor —
// deliberately, so the single best-ranked customer always lands in
// bucket 5 regardless of how few customers the org has. A shop with only
// 2-3 customers (very plausible for this market at MVP stage) still needs
// its best customer classified as a champion; a floor-based formula tops
// out below 5 for any n<5, which would make the "champion" segment
// unreachable for every small shop using this feature — the exact
// customers this feature matters most for.
func quantileScores(values map[uuid.UUID]float64) map[uuid.UUID]int {
	n := len(values)
	scores := make(map[uuid.UUID]int, n)
	if n == 0 {
		return scores
	}

	type kv struct {
		id  uuid.UUID
		val float64
	}
	sorted := make([]kv, 0, n)
	for id, v := range values {
		sorted = append(sorted, kv{id: id, val: v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].val < sorted[j].val })

	for i, item := range sorted {
		rank := i + 1 // 1-indexed: rank n is the largest value
		// Ceiling division: ceil(a/b) == (a+b-1)/b for positive ints.
		score := (rank*5 + n - 1) / n
		if score < 1 {
			score = 1
		}
		if score > 5 {
			score = 5
		}
		scores[item.id] = score
	}
	return scores
}

// classifySegment maps a Recency/Frequency score pair to one of the five
// RFM segments. Order matters — champion is checked before loyal, etc.,
// since the ranges overlap by design (e.g. r=5,f=5 satisfies every clause
// below it too). Exhaustive over every (r,f) in [1,5]x[1,5]: verified by
// hand, see the package's rfm_test.go.
func classifySegment(r, f int) entity.RFMSegment {
	switch {
	case r >= 4 && f >= 4:
		return entity.RFMSegmentChampion
	case r >= 3 && f >= 3:
		return entity.RFMSegmentLoyal
	case r <= 2 && f >= 3:
		return entity.RFMSegmentAtRisk
	case r >= 3 && f <= 2:
		return entity.RFMSegmentSleeping
	default:
		return entity.RFMSegmentLost
	}
}
