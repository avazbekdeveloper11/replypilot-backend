// Command worker-ai is the AI reply pipeline's worker process — the
// consumer side of the dm.received event cmd/api's webhook receiver
// publishes (see instagram.WebhookUseCase's doc comment). It is a
// completely separate binary/process/deployable from cmd/api, sharing only
// the same Postgres database and RabbitMQ broker — this is what lets a
// slow Gemini API call or a Meta API hiccup never block webhook ingestion.
//
// Run it alongside the API service (a second `go run` / container /
// docker-compose service) — starting only cmd/api is not enough for the AI
// to actually reply to anything; the message will sit ingested and
// published but never picked up. See backend/README.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/replypilot/backend/internal/config"
	"github.com/replypilot/backend/internal/integration/geminiapi"
	"github.com/replypilot/backend/internal/integration/metaapi"
	"github.com/replypilot/backend/internal/platform/database"
	"github.com/replypilot/backend/internal/platform/geminikey"
	platformlogger "github.com/replypilot/backend/internal/platform/logger"
	"github.com/replypilot/backend/internal/platform/queue"
	postgresrepo "github.com/replypilot/backend/internal/repository/postgres"
	aiuc "github.com/replypilot/backend/internal/usecase/ai"
	knowledgebaseuc "github.com/replypilot/backend/internal/usecase/knowledgebase"
	platformsettingsuc "github.com/replypilot/backend/internal/usecase/platformsettings"
	"github.com/replypilot/backend/pkg/crypto"
)

// queueName is this worker's own durable queue bound to
// queue.RoutingKeyDMReceived. Named so multiple worker instances share load
// (competing consumers on the same queue) rather than each instance getting
// its own copy of every event.
const queueName = "worker_ai.dm_received"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, err := platformlogger.New(cfg.App.Env)
	if err != nil {
		return fmt.Errorf("build logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	gormLogger := gormlogger.Default.LogMode(gormlogger.Warn)
	if cfg.App.Env != "production" {
		gormLogger = gormlogger.Default.LogMode(gormlogger.Info)
	}
	db, err := database.New(cfg.DB, gormLogger)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}

	mqConn, err := queue.New(cfg.RabbitMQ)
	if err != nil {
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	defer func() { _ = mqConn.Close() }()

	consumer, err := queue.NewConsumer(mqConn, queueName, queue.RoutingKeyDMReceived)
	if err != nil {
		return fmt.Errorf("declare consumer queue: %w", err)
	}

	encryptor, err := crypto.NewAESGCMEncryptor([]byte(cfg.Security.TokenEncryptionKey))
	if err != nil {
		return fmt.Errorf("build token encryptor: %w", err)
	}

	// --- repositories ---
	convRepo := postgresrepo.NewConversationRepository(db)
	msgRepo := postgresrepo.NewMessageRepository(db)
	accountRepo := postgresrepo.NewInstagramAccountRepository(db)
	aiRespRepo := postgresrepo.NewAIResponseRepository(db)
	knowledgeDocRepo := postgresrepo.NewKnowledgeDocumentRepository(db)
	knowledgeChunkRepo := postgresrepo.NewKnowledgeChunkRepository(db)
	platformSettingsRepo := postgresrepo.NewPlatformSettingsRepository(db)
	productRepo := postgresrepo.NewProductRepository(db)
	clickIntegrationRepo := postgresrepo.NewClickIntegrationRepository(db)

	// --- integrations ---
	geminiClient := geminiapi.NewClient(cfg.Gemini.APIKey)
	metaClient := metaapi.NewClient(cfg.Meta.AppID, cfg.Meta.AppSecret, cfg.Meta.RedirectURL, cfg.Meta.GraphAPIBaseURL)

	// --- usecases ---
	// knowledgeUseCase is only used here for its Search method (RAG
	// retrieval) — Upload/List/Get/Delete are the dashboard-facing side of
	// this same usecase, not relevant to this worker, but the constructor
	// still needs both repositories since New() doesn't offer a
	// retrieval-only constructor.
	knowledgeUseCase := knowledgebaseuc.New(knowledgeDocRepo, knowledgeChunkRepo, geminiClient)
	aiUseCase := aiuc.New(convRepo, msgRepo, accountRepo, aiRespRepo, knowledgeUseCase, geminiClient, metaClient, encryptor, productRepo, clickIntegrationRepo)
	platformSettingsUseCase := platformsettingsuc.New(platformSettingsRepo, encryptor)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Same reasoning as cmd/api's identical call — a Gemini key rotated
	// from the admin panel must reach the AI reply pipeline too, not just
	// the API service's knowledge-base uploads. See
	// internal/platform/geminikey's doc comment.
	geminikey.StartRefresher(
		ctx, logger,
		platformSettingsUseCase.ResolveGeminiAPIKey,
		geminiClient.SetAPIKey,
		cfg.Gemini.APIKey,
		geminikey.DefaultInterval,
	)

	logger.Info("worker-ai: listening", zap.String("queue", queueName), zap.String("routing_key", queue.RoutingKeyDMReceived))

	err = consumer.Run(ctx, func(ctx context.Context, body []byte) error {
		return handleDelivery(ctx, logger, aiUseCase, body)
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("consumer stopped: %w", err)
	}

	logger.Info("worker-ai: shutdown signal received, stopped consuming")
	return nil
}
