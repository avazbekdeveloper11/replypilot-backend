// Package signature verifies inbound webhook authenticity. The Meta
// implementation here is the concrete requirement from this task; the same
// pattern (HMAC-SHA256 over the raw body, constant-time compare) applies to
// Stripe/Telegram webhooks if those are added later.
package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

var ErrInvalidSignature = errors.New("signature: invalid or missing X-Hub-Signature-256")

// VerifyMetaSignature validates the X-Hub-Signature-256 header Meta sends
// with every webhook delivery, per
// https://developers.facebook.com/docs/messenger-platform/webhooks#security
//
// header is the raw header value, e.g. "sha256=abcdef...".
// appSecret is the Meta App Secret (Settings > Basic in the app dashboard),
// never the page/user access token — a common and dangerous mix-up.
// body MUST be the exact raw request bytes read before any JSON
// unmarshaling. Verifying against a re-serialized/re-parsed body will fail
// even for a legitimate request, since re-encoding can change whitespace,
// key order, or number formatting byte-for-byte.
func VerifyMetaSignature(header, appSecret string, body []byte) error {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return ErrInvalidSignature
	}
	expectedHex := strings.TrimPrefix(header, prefix)

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	computedHex := hex.EncodeToString(mac.Sum(nil))

	// hmac.Equal (not ==) — constant-time comparison so a timing attack
	// can't be used to guess the signature byte by byte.
	if !hmac.Equal([]byte(computedHex), []byte(expectedHex)) {
		return ErrInvalidSignature
	}
	return nil
}
