// Package logger provides the single structured-logging entry point for the
// whole service. Every component receives a *zap.Logger via DI rather than
// calling a global — that keeps log fields (request_id, org_id, ...)
// scoped correctly per request instead of leaking across goroutines.
package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds a zap.Logger appropriate for the given environment.
// production: JSON output, ISO8601 timestamps, info level — what a log
// aggregator (Loki/CloudWatch/Datadog) expects.
// anything else: human-readable console output with color, debug level.
func New(env string) (*zap.Logger, error) {
	if env == "production" {
		cfg := zap.NewProductionConfig()
		cfg.EncoderConfig.TimeKey = "timestamp"
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		return cfg.Build()
	}

	cfg := zap.NewDevelopmentConfig()
	cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	return cfg.Build()
}
