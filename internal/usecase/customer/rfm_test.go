package customer

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/replypilot/backend/internal/domain/entity"
)

// TestClassifySegmentExhaustive backs the "verified by hand" claim in
// classifySegment's doc comment: every (r,f) pair in [1,5]x[1,5] must map
// to exactly one non-empty segment, and the ranges must line up with the
// hand-checked table in the doc comment.
func TestClassifySegmentExhaustive(t *testing.T) {
	want := map[[2]int]entity.RFMSegment{
		{1, 1}: entity.RFMSegmentLost, {1, 2}: entity.RFMSegmentLost, {1, 3}: entity.RFMSegmentAtRisk, {1, 4}: entity.RFMSegmentAtRisk, {1, 5}: entity.RFMSegmentAtRisk,
		{2, 1}: entity.RFMSegmentLost, {2, 2}: entity.RFMSegmentLost, {2, 3}: entity.RFMSegmentAtRisk, {2, 4}: entity.RFMSegmentAtRisk, {2, 5}: entity.RFMSegmentAtRisk,
		{3, 1}: entity.RFMSegmentSleeping, {3, 2}: entity.RFMSegmentSleeping, {3, 3}: entity.RFMSegmentLoyal, {3, 4}: entity.RFMSegmentLoyal, {3, 5}: entity.RFMSegmentLoyal,
		{4, 1}: entity.RFMSegmentSleeping, {4, 2}: entity.RFMSegmentSleeping, {4, 3}: entity.RFMSegmentLoyal, {4, 4}: entity.RFMSegmentChampion, {4, 5}: entity.RFMSegmentChampion,
		{5, 1}: entity.RFMSegmentSleeping, {5, 2}: entity.RFMSegmentSleeping, {5, 3}: entity.RFMSegmentLoyal, {5, 4}: entity.RFMSegmentChampion, {5, 5}: entity.RFMSegmentChampion,
	}
	for rf, want := range want {
		got := classifySegment(rf[0], rf[1])
		if got != want {
			t.Errorf("classifySegment(%d, %d) = %q, want %q", rf[0], rf[1], got, want)
		}
	}
}

func TestQuantileScoresEmpty(t *testing.T) {
	if got := quantileScores(map[uuid.UUID]float64{}); len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestQuantileScoresRangeAndOrder(t *testing.T) {
	ids := make([]uuid.UUID, 11)
	values := make(map[uuid.UUID]float64, 11)
	for i := range ids {
		ids[i] = uuid.New()
		values[ids[i]] = float64(i) // 0..10, strictly increasing
	}

	scores := quantileScores(values)
	if len(scores) != len(ids) {
		t.Fatalf("expected %d scores, got %d", len(ids), len(scores))
	}

	for _, id := range ids {
		s := scores[id]
		if s < 1 || s > 5 {
			t.Errorf("score %d out of [1,5] range", s)
		}
	}

	// Monotonic: a strictly larger input value must never receive a
	// strictly smaller score.
	for i := 1; i < len(ids); i++ {
		if scores[ids[i]] < scores[ids[i-1]] {
			t.Errorf("score not monotonic at index %d: %d < %d", i, scores[ids[i]], scores[ids[i-1]])
		}
	}

	// Highest value must land in the top bucket.
	if scores[ids[len(ids)-1]] != 5 {
		t.Errorf("expected highest value to score 5, got %d", scores[ids[len(ids)-1]])
	}
}

func TestAssignRFMSegmentsNoOrders(t *testing.T) {
	summaries := []*entity.CustomerSummary{
		{ConversationID: uuid.New(), PaidOrderCount: 0},
	}
	assignRFMSegments(summaries)
	if summaries[0].Segment != entity.RFMSegmentNew {
		t.Errorf("expected RFMSegmentNew, got %q", summaries[0].Segment)
	}
}

func TestAssignRFMSegmentsSingleCustomerWithOrders(t *testing.T) {
	now := time.Now()
	summaries := []*entity.CustomerSummary{
		{
			ConversationID: uuid.New(),
			PaidOrderCount: 3,
			TotalPaidCents: 100000,
			LastPaidAt:     &now,
		},
	}
	assignRFMSegments(summaries)
	s := summaries[0]
	// A lone customer is simultaneously the min and max of every metric,
	// so quantileScores must place them in the top bucket for all three.
	if s.RecencyScore != 5 || s.FrequencyScore != 5 || s.MonetaryScore != 5 {
		t.Errorf("expected all scores = 5 for a single customer, got r=%d f=%d m=%d", s.RecencyScore, s.FrequencyScore, s.MonetaryScore)
	}
	if s.Segment != entity.RFMSegmentChampion {
		t.Errorf("expected champion, got %q", s.Segment)
	}
}
