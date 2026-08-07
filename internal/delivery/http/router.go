// Package httpserver owns HTTP routing and the server lifecycle. It is
// named httpserver, not http, deliberately — living in a directory called
// http while also being named http would shadow net/http's package
// identifier in every file that needs to import both.
package httpserver

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/replypilot/backend/docs" // side-effect import: registers the swagger spec
	"github.com/replypilot/backend/internal/delivery/http/middleware"
	v1 "github.com/replypilot/backend/internal/delivery/http/v1"
)

// Handlers bundles every v1 handler the router wires up. Constructed once
// in internal/di and passed in — this file owns routing, not object
// construction.
type Handlers struct {
	Auth              *v1.AuthHandler
	Organization      *v1.OrganizationHandler
	Instagram         *v1.InstagramHandler
	Telegram          *v1.TelegramHandler
	Webhook           *v1.WebhookHandler
	TelegramWebhook   *v1.TelegramWebhookHandler
	ClickWebhook      *v1.ClickWebhookHandler
	Conversation      *v1.ConversationHandler
	Dashboard         *v1.DashboardHandler
	Team              *v1.TeamHandler
	Knowledge         *v1.KnowledgeHandler
	Billing           *v1.BillingHandler
	Analytics         *v1.AnalyticsHandler
	User              *v1.UserHandler
	Admin             *v1.AdminHandler
	Product           *v1.ProductHandler
	Click             *v1.ClickHandler
	Lead              *v1.LeadHandler
	Insights          *v1.InsightsHandler
	CommentAutomation *v1.CommentAutomationHandler
	Campaign          *v1.CampaignHandler
	Customer          *v1.CustomerHandler
	AmoCRM            *v1.AmoCRMHandler
}

// Middlewares bundles the gin.HandlerFuncs that need constructed
// dependencies (a *zap.Logger, a *redis.Client, a *jwtutil.Manager) — built
// in internal/di, where those dependencies already exist, rather than each
// middleware reaching into a global.
type Middlewares struct {
	Recovery           gin.HandlerFunc
	Logging            gin.HandlerFunc
	CORS               gin.HandlerFunc
	Auth               gin.HandlerFunc
	RateLimitByIP      gin.HandlerFunc
	RateLimitByOrg     gin.HandlerFunc
	RequirePlatformAdmin gin.HandlerFunc
}

// @title                       ReplyPilot API
// @version                     1.0
// @description                 AI-powered Instagram DM Sales Agent — API for dashboard clients and the Meta webhook.
// @BasePath                    /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Type "Bearer" followed by a space and the access token.
func NewRouter(h Handlers, mw Middlewares) *gin.Engine {
	r := gin.New()

	// Order matters: Recovery must be outermost so a panic anywhere below
	// is caught; ErrorHandler must run after the handler (via c.Next()) to
	// see whatever it put in c.Errors.
	r.Use(
		mw.Recovery,
		middleware.RequestID(),
		mw.Logging,
		middleware.ErrorHandler(),
		mw.CORS,
	)

	r.GET("/healthz", func(c *gin.Context) { c.Status(200) })
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Meta webhook: no JWT — Meta is not a logged-in user. Authenticated
	// instead via HMAC signature inside WebhookHandler.Receive, and
	// rate-limited by IP since there's no tenant context yet.
	webhooks := r.Group("/webhooks", mw.RateLimitByIP)
	webhooks.GET("/meta", h.Webhook.VerifySubscription)
	webhooks.POST("/meta", h.Webhook.Receive)
	webhooks.POST("/stripe", h.Billing.Webhook)
	// No JWT, same reasoning as /webhooks/meta above — Telegram is not a
	// logged-in user either. Authenticated via the X-Telegram-Bot-Api-Secret-Token
	// header instead (see TelegramWebhookHandler.Receive / config.TelegramConfig).
	webhooks.POST("/telegram/:id", h.TelegramWebhook.Receive)
	// No JWT, same reasoning as the two webhooks above — Click is not a
	// logged-in user. Authenticated instead via the per-org secret key
	// signature check inside payment.WebhookUseCase.Process (there is no
	// shared platform-wide secret here, unlike Telegram's header: Click
	// posts one org's service_id in the body itself, and that org's own
	// secret key is what verifies it — see ClickIntegration.SecretKeyEncrypted).
	webhooks.POST("/click", h.ClickWebhook.Receive)

	v1Group := r.Group("/v1")
	{
		auth := v1Group.Group("/auth", mw.RateLimitByIP)
		auth.POST("/register/code", h.Auth.RequestRegistrationCode)
		auth.POST("/register", h.Auth.Register)
		auth.GET("/organizations", h.Auth.ListOrganizations)
		auth.POST("/login", h.Auth.Login)
		auth.POST("/refresh", h.Auth.Refresh)
		auth.POST("/logout", h.Auth.Logout)
		auth.POST("/password/forgot", h.Auth.ForgotPassword)
		auth.POST("/password/reset", h.Auth.ResetPassword)

		// Deliberately public, not under `protected` — Meta's OAuth
		// redirect (or this app's frontend proxying it, see
		// web_app/replypilot-web's instagram callback page) hits this
		// with no Authorization header at all. Safety comes from the
		// CSRF `state` param, resolved against the server-side store
		// inside InstagramHandler.Callback -> OAuthUseCase.Complete —
		// see that handler's doc comment, which already documented this
		// as the intended design. Previously (incorrectly) nested under
		// `protected`, which made it unreachable by an actual Meta
		// redirect (401 before the handler ever ran) — fixed here.
		instagramPublic := v1Group.Group("/instagram", mw.RateLimitByIP)
		instagramPublic.GET("/callback", h.Instagram.Callback)

		// Same reasoning as the Instagram callback above — amoCRM's OAuth
		// redirect (or this app's frontend proxying it) hits this with no
		// Authorization header. Safety comes from the CSRF `state` param.
		amocrmPublic := v1Group.Group("/amocrm", mw.RateLimitByIP)
		amocrmPublic.GET("/callback", h.AmoCRM.Callback)

		protected := v1Group.Group("", mw.Auth, mw.RateLimitByOrg)
		{
			protected.GET("/organizations/me", h.Organization.Me)
			protected.PATCH("/organizations/me", h.Organization.UpdateMe)
			protected.PATCH("/organizations/me/business-hours", h.Organization.UpdateBusinessHours)

			protected.GET("/users/me", h.User.Me)
			protected.PATCH("/users/me", h.User.UpdateProfile)
			protected.POST("/users/me/change-password", h.User.ChangePassword)

			protected.POST("/instagram/connect", h.Instagram.Connect)
			protected.GET("/instagram/accounts", h.Instagram.List)
			protected.DELETE("/instagram/accounts/:id", h.Instagram.Disconnect)

			protected.POST("/telegram/connect", h.Telegram.Connect)
			protected.GET("/telegram/accounts", h.Telegram.List)
			protected.DELETE("/telegram/accounts/:id", h.Telegram.Disconnect)

			protected.GET("/conversations", h.Conversation.List)
			protected.GET("/conversations/:id", h.Conversation.Get)
			protected.GET("/conversations/:id/messages", h.Conversation.ListMessages)
			protected.POST("/conversations/:id/messages", h.Conversation.SendMessage)
			protected.PATCH("/conversations/:id/take-over", h.Conversation.TakeOver)
			protected.PATCH("/conversations/:id/resolve", h.Conversation.Resolve)
			protected.POST("/conversations/:id/summary", h.Conversation.Summarize)

			protected.GET("/dashboard/stats", h.Dashboard.Stats)
			protected.GET("/dashboard/timeseries", h.Dashboard.TimeSeries)
			protected.GET("/dashboard/ai-performance", h.Dashboard.AIPerformance)
			protected.GET("/dashboard/notifications", h.Dashboard.Notifications)

			protected.GET("/team/members", h.Team.List)
			protected.POST("/team/members", h.Team.Invite)
			protected.PATCH("/team/members/:id", h.Team.UpdateRole)
			protected.DELETE("/team/members/:id", h.Team.Remove)
			protected.GET("/team/roles", h.Team.Roles)

			protected.GET("/knowledge-base/documents", h.Knowledge.List)
			protected.POST("/knowledge-base/documents", h.Knowledge.Upload)
			protected.GET("/knowledge-base/documents/:id", h.Knowledge.Get)
			protected.PATCH("/knowledge-base/documents/:id", h.Knowledge.Update)
			protected.DELETE("/knowledge-base/documents/:id", h.Knowledge.Delete)

			protected.GET("/billing/plans", h.Billing.ListPlans)
			protected.GET("/billing/subscription", h.Billing.GetSubscription)
			protected.POST("/billing/checkout-session", h.Billing.CreateCheckoutSession)
			protected.POST("/billing/portal-session", h.Billing.CreatePortalSession)

			protected.GET("/analytics/response-time", h.Analytics.ResponseTime)
			protected.GET("/analytics/ai-usage", h.Analytics.AIUsage)
			protected.GET("/analytics/conversation-outcomes", h.Analytics.ConversationOutcomes)
			protected.GET("/analytics/ai-insights", h.Insights.Get)
			protected.POST("/analytics/ai-insights/regenerate", h.Insights.Regenerate)

			protected.GET("/products", h.Product.List)
			protected.POST("/products", h.Product.Create)
			protected.POST("/products/import", h.Product.Import)
			protected.PATCH("/products/:id", h.Product.Update)
			protected.DELETE("/products/:id", h.Product.Delete)

			protected.GET("/integrations/click", h.Click.Get)
			protected.POST("/integrations/click/connect", h.Click.Connect)
			protected.POST("/integrations/click/disconnect", h.Click.Disconnect)

			protected.POST("/amocrm/connect", h.AmoCRM.Connect)
			protected.GET("/integrations/amocrm", h.AmoCRM.Status)
			protected.POST("/integrations/amocrm/disconnect", h.AmoCRM.Disconnect)

			protected.GET("/integrations/comment-automation", h.CommentAutomation.Get)
			protected.PUT("/integrations/comment-automation", h.CommentAutomation.Update)

			protected.POST("/campaigns/draft", h.Campaign.Draft)
			protected.POST("/campaigns/send", h.Campaign.Send)

			protected.GET("/customers", h.Customer.List)
			protected.GET("/customers/:conversation_id/orders", h.Customer.Orders)
			protected.POST("/customers/:conversation_id/amocrm-sync", h.AmoCRM.Sync)

			protected.GET("/leads", h.Lead.List)
			protected.PATCH("/leads/:id", h.Lead.UpdateStatus)

			// Cross-tenant by design — gated by RequirePlatformAdmin on top
			// of the same Auth+RateLimitByOrg every other protected route
			// gets. See middleware.RequirePlatformAdmin's doc comment.
			admin := protected.Group("/admin", mw.RequirePlatformAdmin)
			admin.GET("/organizations", h.Admin.ListOrganizations)
			admin.POST("/organizations/:id/suspend", h.Admin.Suspend)
			admin.POST("/organizations/:id/reactivate", h.Admin.Reactivate)
			admin.GET("/stats", h.Admin.Stats)
			admin.GET("/settings/gemini", h.Admin.GetGeminiSettings)
			admin.PUT("/settings/gemini", h.Admin.SetGeminiSettings)
		}
	}

	return r
}
