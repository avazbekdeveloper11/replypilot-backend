// Command token-refresh is a run-once-and-exit job: it finds every
// connected InstagramAccount (across all organizations) whose long-lived
// access token is within refreshWindow of expiring, calls Meta's
// refresh_access_token endpoint for each, and persists the new encrypted
// token + expiry. It is not a long-running service — no signal handling,
// no loop — it does one pass and exits, meant to be invoked by an external
// scheduler (cron, a Kubernetes CronJob, etc.) on a daily cadence. See
// backend/README.md's deployment section for the recommended schedule.
//
// Why this needs to exist at all: metaapi.Client.RefreshLongLivedToken
// already existed from day one (see that method's doc comment, which
// always described "a scheduled job (not part of this API service)") but
// nothing ever called it — every InstagramAccount's long-lived token was
// silently ticking down to its ~60-day expiry with no renewal path. Once a
// token expired, internal/usecase/ai.handleSendFailure would flip the
// account to "expired" on the next failed send, but by then the DM
// pipeline for that account is already dead until the user manually
// reconnects. This job is what's supposed to prevent that from happening
// in the first place.
//
// Exit code is 0 unless a setup/config/DB-connection failure prevented the
// job from running at all — a single account's refresh failing (e.g.
// because Meta already invalidated the token for a reason a refresh can't
// fix) is logged and does not fail the run; see refreshOne.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/replypilot/backend/internal/config"
	"github.com/replypilot/backend/internal/domain/entity"
	"github.com/replypilot/backend/internal/integration/metaapi"
	"github.com/replypilot/backend/internal/platform/database"
	platformlogger "github.com/replypilot/backend/internal/platform/logger"
	postgresrepo "github.com/replypilot/backend/internal/repository/postgres"
	"github.com/replypilot/backend/pkg/crypto"
)

// refreshWindow matches the ~10 day lead time
// metaapi.Client.RefreshLongLivedToken's doc comment and the
// idx_instagram_accounts_token_expiry index were both already designed
// around — see database/schema.sql and that method's comment. Run this job
// daily (or more often) and every account gets several attempts before its
// token actually expires, so one failed Meta API call on a given day isn't
// fatal to the account.
const refreshWindow = 10 * 24 * time.Hour

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "token-refresh:", err)
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

	encryptor, err := crypto.NewAESGCMEncryptor([]byte(cfg.Security.TokenEncryptionKey))
	if err != nil {
		return fmt.Errorf("build token encryptor: %w", err)
	}

	accountRepo := postgresrepo.NewInstagramAccountRepository(db)
	metaClient := metaapi.NewClient(cfg.Meta.AppID, cfg.Meta.AppSecret, cfg.Meta.RedirectURL, cfg.Meta.GraphAPIBaseURL)

	ctx := context.Background()

	accounts, err := accountRepo.ListNearingExpiry(ctx, refreshWindow)
	if err != nil {
		return fmt.Errorf("list accounts nearing expiry: %w", err)
	}

	logger.Info("token-refresh: starting run",
		zap.Int("accounts_due", len(accounts)),
		zap.Duration("refresh_window", refreshWindow),
	)

	var refreshed, failed int
	for _, account := range accounts {
		if refreshOne(ctx, logger, accountRepo, metaClient, encryptor, account) {
			refreshed++
		} else {
			failed++
		}
	}

	logger.Info("token-refresh: run complete",
		zap.Int("accounts_due", len(accounts)),
		zap.Int("refreshed", refreshed),
		zap.Int("failed", failed),
	)
	return nil
}

// refreshOne handles a single account end-to-end and never returns an
// error — a per-account failure is logged and the run moves on, per the
// package doc comment's exit-code policy. Returns true on success.
func refreshOne(
	ctx context.Context,
	logger *zap.Logger,
	accountRepo *postgresrepo.InstagramAccountRepository,
	metaClient *metaapi.Client,
	encryptor *crypto.AESGCMEncryptor,
	account *entity.InstagramAccount,
) bool {
	log := logger.With(
		zap.String("instagram_account_id", account.ID.String()),
		zap.String("organization_id", account.OrganizationID.String()),
	)

	currentToken, err := encryptor.Decrypt(account.AccessTokenEncrypted)
	if err != nil {
		log.Error("token-refresh: decrypt stored token failed", zap.Error(err))
		return false
	}

	newToken, expiresIn, err := metaClient.RefreshLongLivedToken(ctx, currentToken)
	if err != nil {
		// A 190 here means Meta already invalidated this token for a reason a
		// refresh can't fix (revoked, password changed, etc.) — the same
		// signal internal/usecase/ai.handleSendFailure reacts to on a failed
		// send. Reacting to it here too means an account that's been silently
		// broken since its last DM (no inbound message to trigger the
		// send-failure path) still gets its status corrected by the next
		// scheduled run, instead of sitting at "connected" indefinitely.
		var graphErr *metaapi.GraphAPIError
		if errors.As(err, &graphErr) && graphErr.IsAuthError() {
			newStatus := entity.InstagramAccountStatusRevoked
			if graphErr.IsExpired() {
				newStatus = entity.InstagramAccountStatusExpired
			}
			if account.Status != newStatus {
				account.Status = newStatus
				if updErr := accountRepo.Update(ctx, account); updErr != nil {
					log.Error("token-refresh: refresh rejected by meta, and status flip failed",
						zap.Error(err), zap.NamedError("status_update_error", updErr))
					return false
				}
			}
			log.Warn("token-refresh: refresh rejected by meta, account marked for reconnect",
				zap.String("new_status", string(newStatus)), zap.Error(err))
			return false
		}
		log.Error("token-refresh: refresh call failed", zap.Error(err))
		return false
	}

	encryptedToken, err := encryptor.Encrypt(newToken)
	if err != nil {
		log.Error("token-refresh: encrypt refreshed token failed", zap.Error(err))
		return false
	}

	expiresAt := time.Now().Add(expiresIn)
	account.AccessTokenEncrypted = encryptedToken
	account.TokenExpiresAt = &expiresAt
	// The account was already status=connected — ListNearingExpiry only
	// returns those — but set it explicitly rather than leaving it
	// implicit, in case that invariant ever changes.
	account.Status = entity.InstagramAccountStatusConnected

	if err := accountRepo.Update(ctx, account); err != nil {
		log.Error("token-refresh: persist refreshed token failed", zap.Error(err))
		return false
	}

	log.Info("token-refresh: refreshed", zap.Time("new_expires_at", expiresAt))
	return true
}
