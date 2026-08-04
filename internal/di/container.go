// Package di is the composition root — the one place in the codebase that
// knows about every concrete implementation (GORM repositories, the Redis
// refresh-token store, the Meta API client) and wires them behind the
// interfaces everything else depends on. Nothing outside this package
// imports gorm, redis, or amqp directly except the platform/* packages
// that establish the raw connections.
//
// Dependency injection here is plain constructor injection — new(X, Y, Z)
// — not a DI framework/container library. At this codebase's size a
// framework would add indirection without adding value; reach for one only
// if wiring genuinely becomes unmanageable by hand.
package di

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/replypilot/backend/internal/config"
	httpserver "github.com/replypilot/backend/internal/delivery/http"
	"github.com/replypilot/backend/internal/delivery/http/middleware"
	v1 "github.com/replypilot/backend/internal/delivery/http/v1"
	"github.com/replypilot/backend/internal/integration/geminiapi"
	"github.com/replypilot/backend/internal/integration/metaapi"
	"github.com/replypilot/backend/internal/integration/resendapi"
	"github.com/replypilot/backend/internal/integration/stripeapi"
	"github.com/replypilot/backend/internal/platform/cache"
	"github.com/replypilot/backend/internal/platform/database"
	"github.com/replypilot/backend/internal/platform/geminikey"
	platformlogger "github.com/replypilot/backend/internal/platform/logger"
	"github.com/replypilot/backend/internal/platform/notify"
	"github.com/replypilot/backend/internal/platform/queue"
	postgresrepo "github.com/replypilot/backend/internal/repository/postgres"
	redisrepo "github.com/replypilot/backend/internal/repository/redis"
	authuc "github.com/replypilot/backend/internal/usecase/auth"
	conversationuc "github.com/replypilot/backend/internal/usecase/conversation"
	adminuc "github.com/replypilot/backend/internal/usecase/admin"
	analyticsuc "github.com/replypilot/backend/internal/usecase/analytics"
	billinguc "github.com/replypilot/backend/internal/usecase/billing"
	dashboarduc "github.com/replypilot/backend/internal/usecase/dashboard"
	instagramuc "github.com/replypilot/backend/internal/usecase/instagram"
	knowledgebaseuc "github.com/replypilot/backend/internal/usecase/knowledgebase"
	organizationuc "github.com/replypilot/backend/internal/usecase/organization"
	platformsettingsuc "github.com/replypilot/backend/internal/usecase/platformsettings"
	teamuc "github.com/replypilot/backend/internal/usecase/team"
	useruc "github.com/replypilot/backend/internal/usecase/user"
	"github.com/replypilot/backend/pkg/crypto"
	"github.com/replypilot/backend/pkg/jwtutil"
)

type Container struct {
	Config *config.Config
	Logger *zap.Logger
	Router *gin.Engine

	db    *gorm.DB
	redis *redis.Client
	mq    *queue.Connection

	// Held only so StartGeminiKeyRefresher can wire them together after
	// New returns — not exposed as exported fields since nothing outside
	// this package should reach into a live usecase/client instance
	// directly (that's what Handlers/Router are for).
	geminiClient            *geminiapi.Client
	platformSettingsUseCase *platformsettingsuc.UseCase
}

func New(cfg *config.Config) (*Container, error) {
	logger, err := platformlogger.New(cfg.App.Env)
	if err != nil {
		return nil, fmt.Errorf("build logger: %w", err)
	}

	db, err := database.New(cfg.DB, gormLoggerFor(cfg.App.Env))
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}

	redisClient, err := cache.New(cfg.Redis)
	if err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	mqConn, err := queue.New(cfg.RabbitMQ)
	if err != nil {
		return nil, fmt.Errorf("connect rabbitmq: %w", err)
	}
	publisher := queue.NewPublisher(mqConn)

	encryptor, err := crypto.NewAESGCMEncryptor([]byte(cfg.Security.TokenEncryptionKey))
	if err != nil {
		return nil, fmt.Errorf("build token encryptor: %w", err)
	}

	tokens := jwtutil.NewManager(cfg.JWT.Secret, cfg.JWT.Issuer, cfg.JWT.AccessTokenTTL, cfg.JWT.RefreshTokenTTL)

	// --- repositories ---
	orgRepo := postgresrepo.NewOrganizationRepository(db)
	userRepo := postgresrepo.NewUserRepository(db)
	roleRepo := postgresrepo.NewRoleRepository(db)
	teamMemberRepo := postgresrepo.NewTeamMemberRepository(db)
	instagramAccountRepo := postgresrepo.NewInstagramAccountRepository(db)
	conversationRepo := postgresrepo.NewConversationRepository(db)
	messageRepo := postgresrepo.NewMessageRepository(db)
	webhookLogRepo := postgresrepo.NewWebhookLogRepository(db)
	dashboardRepo := postgresrepo.NewDashboardRepository(db)
	knowledgeDocRepo := postgresrepo.NewKnowledgeDocumentRepository(db)
	knowledgeChunkRepo := postgresrepo.NewKnowledgeChunkRepository(db)
	planRepo := postgresrepo.NewPlanRepository(db)
	subscriptionRepo := postgresrepo.NewSubscriptionRepository(db)
	analyticsRepo := postgresrepo.NewAnalyticsRepository(db)
	adminRepo := postgresrepo.NewAdminRepository(db)
	platformSettingsRepo := postgresrepo.NewPlatformSettingsRepository(db)
	refreshTokenStore := redisrepo.NewRefreshTokenStore(redisClient)
	oauthStateStore := redisrepo.NewOAuthStateStore(redisClient)
	otpStore := redisrepo.NewOTPStore(redisClient)

	// --- integrations ---
	graphClient := metaapi.NewClient(cfg.Meta.AppID, cfg.Meta.AppSecret, cfg.Meta.RedirectURL, cfg.Meta.GraphAPIBaseURL)
	geminiClient := geminiapi.NewClient(cfg.Gemini.APIKey)
	stripeClient := stripeapi.NewClient(cfg.Stripe.SecretKey)

	// EmailSender: real delivery via Resend once RESEND_API_KEY is set,
	// LogNotifier (logs instead of sending) otherwise — see EmailConfig's
	// doc comment in internal/config. Do not run LogNotifier in
	// production; see its own doc comment.
	var emailNotifier notify.EmailSender
	if cfg.Email.ResendAPIKey != "" {
		resendClient := resendapi.NewClient(cfg.Email.ResendAPIKey, cfg.Email.FromEmail)
		emailNotifier = notify.NewResendNotifier(resendClient)
	} else {
		emailNotifier = notify.NewLogNotifier(logger)
	}

	// --- usecases ---
	authUseCase := authuc.New(orgRepo, userRepo, roleRepo, teamMemberRepo, tokens, refreshTokenStore, otpStore, emailNotifier)
	orgUseCase := organizationuc.New(orgRepo)
	oauthUseCase := instagramuc.NewOAuthUseCase(instagramAccountRepo, graphClient, oauthStateStore, encryptor, cfg.Meta.AppID, cfg.Meta.RedirectURL)
	webhookUseCase := instagramuc.NewWebhookUseCase(webhookLogRepo, instagramAccountRepo, conversationRepo, messageRepo, publisher, graphClient, encryptor, cfg.Meta.AppSecret, cfg.Meta.WebhookVerifyToken)
	conversationUseCase := conversationuc.New(conversationRepo, messageRepo)
	dashboardUseCase := dashboarduc.New(dashboardRepo, conversationRepo, instagramAccountRepo)
	teamUseCase := teamuc.New(teamMemberRepo, userRepo, roleRepo)
	knowledgeUseCase := knowledgebaseuc.New(knowledgeDocRepo, knowledgeChunkRepo, geminiClient)
	billingUseCase := billinguc.New(planRepo, subscriptionRepo, userRepo, stripeClient, cfg.App.WebURL, cfg.Stripe.WebhookSecret)
	analyticsUseCase := analyticsuc.New(analyticsRepo)
	userUseCase := useruc.New(userRepo)
	adminUseCase := adminuc.New(adminRepo, orgRepo)
	platformSettingsUseCase := platformsettingsuc.New(platformSettingsRepo, encryptor)

	// --- handlers ---
	handlers := httpserver.Handlers{
		Auth:         v1.NewAuthHandler(authUseCase),
		Organization: v1.NewOrganizationHandler(orgUseCase),
		Instagram:    v1.NewInstagramHandler(oauthUseCase),
		Webhook:      v1.NewWebhookHandler(webhookUseCase),
		Conversation: v1.NewConversationHandler(conversationUseCase),
		Dashboard:    v1.NewDashboardHandler(dashboardUseCase),
		Team:         v1.NewTeamHandler(teamUseCase),
		Knowledge:    v1.NewKnowledgeHandler(knowledgeUseCase),
		Billing:      v1.NewBillingHandler(billingUseCase),
		Analytics:    v1.NewAnalyticsHandler(analyticsUseCase),
		User:         v1.NewUserHandler(userUseCase),
		Admin:        v1.NewAdminHandler(adminUseCase, platformSettingsUseCase),
	}

	// --- middlewares ---
	mw := httpserver.Middlewares{
		Recovery:             middleware.Recovery(logger),
		Logging:              middleware.Logging(logger),
		CORS:                 middleware.CORS(cfg.App.AllowedOrigins),
		Auth:                 middleware.Auth(tokens),
		RateLimitByIP:        middleware.RateLimiter(redisClient, cfg.RateLimit.RequestsPerMinute, middleware.ByIP),
		RateLimitByOrg:       middleware.RateLimiter(redisClient, cfg.RateLimit.RequestsPerMinute, middleware.ByOrganization),
		RequirePlatformAdmin: middleware.RequirePlatformAdmin(),
	}

	if cfg.App.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := httpserver.NewRouter(handlers, mw)

	return &Container{
		Config: cfg,
		Logger: logger,
		Router: router,
		db:     db,
		redis:  redisClient,
		mq:     mqConn,

		geminiClient:            geminiClient,
		platformSettingsUseCase: platformSettingsUseCase,
	}, nil
}

// StartGeminiKeyRefresher begins polling for an admin-configured Gemini
// key (internal/usecase/platformsettings) and applying it to this
// process's Gemini client — see internal/platform/geminikey's doc
// comment for why this exists at all. Call once, after New, with a
// context that's cancelled at shutdown so the background poller doesn't
// outlive the rest of the process's shutdown sequence.
func (c *Container) StartGeminiKeyRefresher(ctx context.Context) {
	geminikey.StartRefresher(
		ctx, c.Logger,
		c.platformSettingsUseCase.ResolveGeminiAPIKey,
		c.geminiClient.SetAPIKey,
		c.Config.Gemini.APIKey,
		geminikey.DefaultInterval,
	)
}

// Close releases every connection this container opened, in reverse
// dependency order. Called from cmd/api/main.go AFTER httpserver.Server.Shutdown
// has finished draining in-flight requests — connections must outlive the
// requests that use them.
func (c *Container) Close() {
	if c.mq != nil {
		if err := c.mq.Close(); err != nil {
			c.Logger.Warn("error closing rabbitmq connection", zap.Error(err))
		}
	}
	if c.redis != nil {
		if err := c.redis.Close(); err != nil {
			c.Logger.Warn("error closing redis connection", zap.Error(err))
		}
	}
	if c.db != nil {
		if sqlDB, err := c.db.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				c.Logger.Warn("error closing database connection", zap.Error(err))
			}
		}
	}
	_ = c.Logger.Sync()
}

// gormLoggerFor keeps GORM's own query logging at Warn (slow queries +
// errors only) in production and Info (every query) in development —
// logging every query in production is both a cost and a noise problem at
// any real traffic volume.
func gormLoggerFor(env string) gormlogger.Interface {
	if env == "production" {
		return gormlogger.Default.LogMode(gormlogger.Warn)
	}
	return gormlogger.Default.LogMode(gormlogger.Info)
}
