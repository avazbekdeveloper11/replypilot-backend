package v1

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/usecase/analytics"
)

type AnalyticsHandler struct {
	uc *analytics.UseCase
}

func NewAnalyticsHandler(uc *analytics.UseCase) *AnalyticsHandler {
	return &AnalyticsHandler{uc: uc}
}

func parseDaysQuery(c *gin.Context, fallback int) (int, error) {
	d := c.Query("days")
	if d == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(d)
	if err != nil || n <= 0 || n > 90 {
		return 0, apperror.InvalidInput("days must be an integer between 1 and 90", err)
	}
	return n, nil
}

// ResponseTime godoc
// @Summary      Daily average first-response time
// @Tags         analytics
// @Produce      json
// @Security     BearerAuth
// @Param        days query int false "trailing window size, 1-90, default 14"
// @Success      200 {object} response.Envelope{data=[]ResponseTimePoint}
// @Router       /v1/analytics/response-time [get]
func (h *AnalyticsHandler) ResponseTime(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	days, err := parseDaysQuery(c, 14)
	if err != nil {
		c.Error(err)
		return
	}

	points, err := h.uc.ResponseTimePerDay(c.Request.Context(), orgID, days)
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]ResponseTimePoint, 0, len(points))
	for _, p := range points {
		out = append(out, ResponseTimePoint{Date: p.Date, AvgSeconds: p.AvgSeconds})
	}
	response.OK(c, out)
}

// AIUsage godoc
// @Summary      Daily AI reply volume, token usage, and confidence
// @Tags         analytics
// @Produce      json
// @Security     BearerAuth
// @Param        days query int false "trailing window size, 1-90, default 14"
// @Success      200 {object} response.Envelope{data=[]AIUsagePoint}
// @Router       /v1/analytics/ai-usage [get]
func (h *AnalyticsHandler) AIUsage(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}
	days, err := parseDaysQuery(c, 14)
	if err != nil {
		c.Error(err)
		return
	}

	points, err := h.uc.AIUsagePerDay(c.Request.Context(), orgID, days)
	if err != nil {
		c.Error(err)
		return
	}
	out := make([]AIUsagePoint, 0, len(points))
	for _, p := range points {
		out = append(out, AIUsagePoint{
			Date:          p.Date,
			ResponseCount: p.ResponseCount,
			TotalTokens:   p.TotalTokens,
			AvgConfidence: p.AvgConfidence,
		})
	}
	response.OK(c, out)
}

// ConversationOutcomes godoc
// @Summary      Snapshot count of conversations by status
// @Tags         analytics
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=ConversationOutcomesResponse}
// @Router       /v1/analytics/conversation-outcomes [get]
func (h *AnalyticsHandler) ConversationOutcomes(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	outcomes, err := h.uc.ConversationOutcomes(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, ConversationOutcomesResponse{
		AIActive:     outcomes.AIActive,
		PendingHuman: outcomes.PendingHuman,
		HumanActive:  outcomes.HumanActive,
		Resolved:     outcomes.Resolved,
		Closed:       outcomes.Closed,
	})
}
