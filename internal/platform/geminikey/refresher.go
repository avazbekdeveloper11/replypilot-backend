// Package geminikey makes an admin-configured Gemini API key
// (internal/usecase/platformsettings) take effect in a running process
// without a restart. Both cmd/api (knowledge-base embeddings) and
// cmd/worker-ai (AI reply generation) construct their own
// *geminiapi.Client at boot from cfg.Gemini.APIKey; without this package,
// rotating the key via the admin panel would silently do nothing until
// both processes were redeployed — the entire point of storing it in the
// DB instead of only .env.
//
// Deliberately takes plain function values (Resolver, an apply func)
// rather than importing usecase/platformsettings or
// internal/integration/geminiapi directly: this is platform-layer
// plumbing, not business logic, and keeping it decoupled from both means
// neither package needs to know this poller exists. Each cmd/* wires the
// concrete resolve (platformsettingsUseCase.ResolveGeminiAPIKey) and apply
// (geminiClient.SetAPIKey) callbacks itself.
package geminikey

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Resolver returns the currently DB-configured key. found=false (not an
// error) means nothing has been set yet — the poller leaves whatever key
// is already applied (the env-var bootstrap value, or a previously
// resolved DB value) untouched in that case, it never "unsets" a key.
type Resolver func(ctx context.Context) (key string, found bool, err error)

// DefaultInterval is how often StartRefresher re-polls. A rotated key
// takes up to this long to reach a running process — short enough that an
// admin rotating a compromised key doesn't have to also restart both
// services to actually cut it off, long enough not to hammer Postgres
// with a query every few seconds across every replica of both processes.
const DefaultInterval = 30 * time.Second

// StartRefresher runs one synchronous poll immediately — so a key already
// configured in the DB is applied before this function returns, meaning
// it's in place before the caller's first real request/message is
// processed — then continues polling in a background goroutine every
// interval until ctx is cancelled.
//
// envFallback is applied once, up front, before the first poll: a
// deployment that has only ever set GEMINI_API_KEY (no admin-panel value
// yet) keeps working exactly as it did before this package existed. Once
// resolve finds a DB value, that takes over; there is no way to fall back
// to the env value again afterward short of restarting the process — see
// platformsettings.UseCase.SetGeminiAPIKey's doc comment on why "unset"
// isn't a supported operation.
func StartRefresher(ctx context.Context, logger *zap.Logger, resolve Resolver, apply func(string), envFallback string, interval time.Duration) {
	if envFallback != "" {
		apply(envFallback)
	}

	last := envFallback
	poll := func() {
		key, found, err := resolve(ctx)
		if err != nil {
			logger.Warn("geminikey: poll failed, keeping current key", zap.Error(err))
			return
		}
		if !found || key == "" || key == last {
			return
		}
		apply(key)
		last = key
		logger.Info("geminikey: applied gemini api key from platform_settings")
	}

	poll()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				poll()
			}
		}
	}()
}
