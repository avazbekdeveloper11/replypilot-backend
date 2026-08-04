// Package notify holds outbound-notification adapters. LogNotifier below
// is the dev/no-provider-configured fallback implementation of
// EmailSender (see email_sender.go) — used when RESEND_API_KEY isn't set,
// so registration and password-reset flows stay exercisable in
// local/staging without a real Resend account.
package notify

import (
	"context"

	"go.uber.org/zap"
)

// LogNotifier writes the email subject/body to the structured application
// log instead of actually sending it.
//
// Before RESEND_API_KEY is configured, this is what backs EmailSender —
// registration and password-reset verification codes land in the log
// instead of an inbox. Rather than silently no-op (which would make those
// flows *look* wired up while doing nothing, indistinguishable from a
// bug) or fake success without a way to retrieve the code (which would
// make them untestable), this logs the full body at Warn level, loud
// enough to notice in local/staging logs, with an explicit comment
// marking it as a placeholder.
//
// Do not run this in production — it puts verification codes in your log
// aggregator, which is a real information-disclosure risk if your log
// pipeline isn't tightly access-controlled. See internal/di/container.go
// for the RESEND_API_KEY-driven switch to ResendNotifier.
type LogNotifier struct {
	logger *zap.Logger
}

func NewLogNotifier(logger *zap.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) Send(_ context.Context, to, subject, htmlBody string) error {
	n.logger.Warn("email send requested — no email provider configured (RESEND_API_KEY unset), logging instead",
		zap.String("to", to),
		zap.String("subject", subject),
		zap.String("html_body", htmlBody),
		zap.String("action_required", "set RESEND_API_KEY to send real email via notify.ResendNotifier"),
	)
	return nil
}
