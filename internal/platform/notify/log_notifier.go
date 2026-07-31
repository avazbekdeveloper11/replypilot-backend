// Package notify holds outbound-notification adapters. Today there is
// exactly one: a log-based stand-in for transactional email, used by the
// forgot-password flow. See LogNotifier's doc comment for the honest
// caveat — this is not an email integration, it's what unblocks
// exercising the reset flow before one exists.
package notify

import (
	"context"

	"go.uber.org/zap"
)

// PasswordResetNotifier is the port internal/usecase/auth depends on —
// "deliver this reset link to this email" — without the usecase knowing
// or caring how. Swap LogNotifier for a real adapter (SES, Postmark,
// Resend, ...) by implementing this same interface; nothing in the
// usecase layer changes.
type PasswordResetNotifier interface {
	Send(ctx context.Context, email, resetLink string) error
}

// LogNotifier writes the reset link to the structured application log
// instead of emailing it.
//
// This project has no email-sending infrastructure (no SMTP config, no
// SES/Postmark/Resend client, no email templates) — building one wasn't
// in scope for this piece of work. Rather than silently no-op (which
// would make the forgot-password flow *look* wired up while actually
// doing nothing, indistinguishable from a bug) or fake success without a
// way to retrieve the link (which would make reset-password untestable),
// this logs the link at Warn level, loud enough to notice in local/staging
// logs, with an explicit comment marking it as a placeholder.
//
// Before shipping this to real users: implement a real
// PasswordResetNotifier and swap it in internal/di/container.go. Do not
// ship LogNotifier to production — it puts a password-reset link in your
// log aggregator, which is a real information-disclosure risk if your log
// pipeline isn't tightly access-controlled.
type LogNotifier struct {
	logger *zap.Logger
}

func NewLogNotifier(logger *zap.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) Send(_ context.Context, email, resetLink string) error {
	n.logger.Warn("password reset requested — no email provider configured, logging link instead",
		zap.String("email", email),
		zap.String("reset_link", resetLink),
		zap.String("action_required", "implement notify.PasswordResetNotifier with a real provider before production"),
	)
	return nil
}
