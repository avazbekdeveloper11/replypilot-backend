// See email_sender.go and log_notifier.go's doc comments for how this
// fits in: ResendNotifier is the real-delivery implementation of
// EmailSender, wired in by internal/di/container.go when RESEND_API_KEY
// is set.
package notify

import (
	"context"

	"github.com/replypilot/backend/internal/integration/resendapi"
)

// ResendNotifier delivers email via Resend (internal/integration/resendapi).
// It's a thin adapter — all it does is satisfy EmailSender by forwarding to
// the Resend client, which owns the actual HTTP call, error shape, and
// sending-domain caveat (see resendapi's package doc comment).
type ResendNotifier struct {
	client *resendapi.Client
}

func NewResendNotifier(client *resendapi.Client) *ResendNotifier {
	return &ResendNotifier{client: client}
}

func (n *ResendNotifier) Send(ctx context.Context, to, subject, htmlBody string) error {
	return n.client.Send(ctx, to, subject, htmlBody)
}
