package v1

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/apperror"
	"github.com/replypilot/backend/internal/usecase/dashboard"
)

type DashboardHandler struct {
	uc *dashboard.UseCase
}

func NewDashboardHandler(uc *dashboard.UseCase) *DashboardHandler {
	return &DashboardHandler{uc: uc}
}

// Stats godoc
// @Summary      Dashboard statistics cards + response time
// @Description  Conversation counts by status, unread count, messages sent today, connected Instagram accounts, and average first-response time over the trailing 30 days.
// @Tags         dashboard
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=DashboardStatsResponse}
// @Router       /v1/dashboard/stats [get]
func (h *DashboardHandler) Stats(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	stats, err := h.uc.Stats(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, DashboardStatsResponse{
		TotalConversations:          stats.Conversations.Total,
		AIActiveConversations:       stats.Conversations.AIActive,
		PendingHumanConversations:   stats.Conversations.PendingHuman,
		HumanActiveConversations:    stats.Conversations.HumanActive,
		ResolvedConversations:       stats.Conversations.Resolved,
		ClosedConversations:         stats.Conversations.Closed,
		UnreadConversations:         stats.Conversations.Unread,
		MessagesToday:               stats.Conversations.MessagesToday,
		ConnectedInstagramAccounts:  stats.ConnectedAccounts,
		AvgFirstResponseSeconds:     stats.AvgFirstResponseSecs,
	})
}

// TimeSeries godoc
// @Summary      Daily conversation counts for the Dashboard chart
// @Tags         dashboard
// @Produce      json
// @Security     BearerAuth
// @Param        days query int false "trailing window size, 1-90, default 7"
// @Success      200 {object} response.Envelope{data=[]DashboardTimeSeriesPoint}
// @Router       /v1/dashboard/timeseries [get]
func (h *DashboardHandler) TimeSeries(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	days := 7
	if d := c.Query("days"); d != "" {
		n, convErr := strconv.Atoi(d)
		if convErr != nil || n <= 0 || n > 90 {
			c.Error(apperror.InvalidInput("days must be an integer between 1 and 90", convErr))
			return
		}
		days = n
	}

	points, err := h.uc.TimeSeries(c.Request.Context(), orgID, days)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]DashboardTimeSeriesPoint, 0, len(points))
	for _, p := range points {
		out = append(out, DashboardTimeSeriesPoint{Date: p.Date, Count: p.Count})
	}
	response.OK(c, out)
}

// AIPerformance godoc
// @Summary      AI performance stats
// @Description  Reads ai_responses directly. This project has no AI reply pipeline implemented yet, so total_responses is 0 today — see docs/DASHBOARD_MILESTONE.md.
// @Tags         dashboard
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=DashboardAIPerformanceResponse}
// @Router       /v1/dashboard/ai-performance [get]
func (h *DashboardHandler) AIPerformance(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	stats, err := h.uc.AIPerformance(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}

	response.OK(c, DashboardAIPerformanceResponse{
		TotalResponses: stats.TotalResponses,
		AvgConfidence:  stats.AvgConfidence,
		AvgLatencyMs:   stats.AvgLatencyMs,
		HandoffRate:    stats.HandoffRate,
	})
}

// Notifications godoc
// @Summary      Recent unread conversations, surfaced as notifications
// @Description  Derived from Conversation.UnreadCount, not a dedicated notifications feed — see docs/DASHBOARD_MILESTONE.md.
// @Tags         dashboard
// @Produce      json
// @Security     BearerAuth
// @Param        limit query int false "max items, default 10"
// @Success      200 {object} response.Envelope{data=[]DashboardNotificationResponse}
// @Router       /v1/dashboard/notifications [get]
func (h *DashboardHandler) Notifications(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	limit := 10
	if l := c.Query("limit"); l != "" {
		n, convErr := strconv.Atoi(l)
		if convErr != nil {
			c.Error(apperror.InvalidInput("limit must be an integer", convErr))
			return
		}
		limit = n
	}

	convs, err := h.uc.Notifications(c.Request.Context(), orgID, limit)
	if err != nil {
		c.Error(err)
		return
	}

	out := make([]DashboardNotificationResponse, 0, len(convs))
	for _, conv := range convs {
		item := DashboardNotificationResponse{
			ConversationID:   conv.ID.String(),
			CustomerUsername: conv.CustomerUsername,
			Preview:          conv.LastMessagePreview,
			UnreadCount:      conv.UnreadCount,
		}
		if conv.LastMessageAt != nil {
			formatted := conv.LastMessageAt.Format(time.RFC3339)
			item.LastMessageAt = &formatted
		}
		out = append(out, item)
	}
	response.OK(c, out)
}
