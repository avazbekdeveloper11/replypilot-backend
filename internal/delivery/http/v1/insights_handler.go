package v1

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/replypilot/backend/internal/delivery/http/response"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/usecase/insights"
)

type InsightsHandler struct {
	uc *insights.UseCase
}

func NewInsightsHandler(uc *insights.UseCase) *InsightsHandler {
	return &InsightsHandler{uc: uc}
}

// Get godoc
// @Summary      Get the organization's cached AI insights
// @Description  Returns 200 with data=null when insights have never been generated — not a 404, same convention as GET /v1/integrations/click. The dashboard treats null data as "show a generate button".
// @Tags         analytics
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=AIInsightsResponse}
// @Router       /v1/analytics/ai-insights [get]
func (h *InsightsHandler) Get(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	ins, err := h.uc.Get(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	if ins == nil {
		response.OK(c, nil)
		return
	}
	response.OK(c, toAIInsightsResponse(ins))
}

// Regenerate godoc
// @Summary      Regenerate the organization's AI insights
// @Description  Recomputes sales/lead/conversation stats fresh and asks Gemini to narrate them alongside a sample of recent customer messages — see insights.UseCase.Regenerate's doc comment. Always overwrites the previous cached result.
// @Tags         analytics
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} response.Envelope{data=AIInsightsResponse}
// @Router       /v1/analytics/ai-insights/regenerate [post]
func (h *InsightsHandler) Regenerate(c *gin.Context) {
	orgID, err := orgIDFromContext(c)
	if err != nil {
		c.Error(err)
		return
	}

	ins, err := h.uc.Regenerate(c.Request.Context(), orgID)
	if err != nil {
		c.Error(err)
		return
	}
	response.OK(c, toAIInsightsResponse(ins))
}

func toAIInsightsResponse(i *entity.AIInsights) AIInsightsResponse {
	return AIInsightsResponse{
		Summary:           i.Summary,
		SalesCount:        i.SalesCount,
		SalesAmountCents:  i.SalesAmountCents,
		LeadCount:         i.LeadCount,
		ConversationCount: i.ConversationCount,
		GeneratedAt:       i.GeneratedAt.Format(time.RFC3339),
	}
}
