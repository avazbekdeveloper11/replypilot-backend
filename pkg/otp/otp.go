// Package otp generates short numeric verification codes — used for
// email-based registration and password-reset verification (see
// internal/usecase/auth). Deliberately not math/rand: a predictable code
// generator would let an attacker skip straight to guessing valid codes
// instead of brute-forcing the 6-digit space, which is exactly the kind
// of shortcut crypto/rand exists to close off (same reasoning as
// pkg/crypto's AES-GCM nonce generation).
package otp

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// codeLength is 6 digits — long enough that brute-forcing within a short
// TTL plus the attempt cap (see internal/repository/redis.OTPStore's doc
// comment) isn't practical, short enough a person can type it from an
// email without a password manager.
const codeLength = 6

// Generate returns a 6-digit numeric code as a zero-padded string (e.g.
// "042817", not "42817") — the leading zero matters for both display
// consistency and so the stored/compared value has a fixed length.
func Generate() (string, error) {
	max := big.NewInt(1_000_000) // 10^6 — exclusive upper bound
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	return fmt.Sprintf("%0*d", codeLength, n.Int64()), nil
}
