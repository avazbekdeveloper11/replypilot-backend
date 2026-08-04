// Package notify holds outbound-notification adapters — today, exactly
// one kind: transactional email for auth verification codes (see
// internal/usecase/auth.UseCase's RequestRegistrationCode and
// ForgotPassword).
package notify

import "context"

// EmailSender is the port internal/usecase/auth depends on — "deliver
// this HTML email to this address" — without the usecase knowing or
// caring which provider sends it. Two implementations exist:
// ResendNotifier (internal/integration/resendapi, real delivery) and
// LogNotifier (this package, dev/no-provider-configured fallback). Which
// one gets wired in is internal/di/container.go's call, driven by whether
// RESEND_API_KEY is set — see that file.
//
// Subject/body content is built by the usecase layer (auth.UseCase), not
// here — this interface is deliberately dumb transport, same split as
// internal/integration/geminiapi.Generator vs. internal/usecase/ai's
// prompt-building.
type EmailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}
