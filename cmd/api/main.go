// Command api is the HTTP API service — the dashboard-facing REST API and
// the Meta webhook receiver. Everything async (AI processing, DM sending,
// Telegram notifications) is a separate binary in the full system
// (cmd/worker-*, described in docs/ARCHITECTURE.md) that consumes off
// RabbitMQ; this binary only ever publishes to it, never consumes.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpserver "github.com/replypilot/backend/internal/delivery/http"

	"github.com/replypilot/backend/internal/config"
	"github.com/replypilot/backend/internal/di"
)

const shutdownTimeout = 15 * time.Second

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

	container, err := di.New(cfg)
	if err != nil {
		return fmt.Errorf("build container: %w", err)
	}
	defer container.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Started before the server so an admin-configured Gemini key (if any)
	// is already applied before this process serves its first knowledge-
	// base upload — see internal/platform/geminikey's doc comment. Bound to
	// ctx so the background poller goroutine stops at shutdown instead of
	// leaking past it.
	container.StartGeminiKeyRefresher(ctx)

	srv := httpserver.NewServer(":"+cfg.App.Port, container.Router, container.Logger)

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
	case <-ctx.Done():
		container.Logger.Info("shutdown signal received, draining in-flight requests")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
	}

	return nil
}
