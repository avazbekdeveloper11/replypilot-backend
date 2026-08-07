package entity

// RFMSegment classifies a customer along Recency and Frequency of paid
// orders into one of five buckets — the standard RFM (Recency/Frequency/
// Monetary) segmentation methodology, computed fresh from
// CustomerSummary rather than stored anywhere (same reasoning as
// CustomerSummary itself: never let a cached label drift from the real
// purchase data).
type RFMSegment string

const (
	// RFMSegmentNew is a customer with zero paid orders — no purchase
	// history exists yet to score, so they're excluded from R/F/M scoring
	// entirely rather than forced into "lost" (they were never a customer
	// to begin with, just a conversation).
	RFMSegmentNew RFMSegment = "new"
	// RFMSegmentChampion: buys often and recently — the org's best
	// customers.
	RFMSegmentChampion RFMSegment = "champion"
	// RFMSegmentLoyal: solid recency and frequency, one step below champion.
	RFMSegmentLoyal RFMSegment = "loyal"
	// RFMSegmentAtRisk: used to buy frequently but hasn't recently —
	// prime cashback/win-back target.
	RFMSegmentAtRisk RFMSegment = "at_risk"
	// RFMSegmentSleeping: bought recently-ish but rarely — low engagement,
	// hasn't built a buying habit.
	RFMSegmentSleeping RFMSegment = "sleeping"
	// RFMSegmentLost: neither recent nor frequent.
	RFMSegmentLost RFMSegment = "lost"
)
