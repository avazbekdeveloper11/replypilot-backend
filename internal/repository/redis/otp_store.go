package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const otpKeyPrefix = "otp:code:"
const otpAttemptsKeyPrefix = "otp:attempts:"

// otpTTL bounds how long a verification code stays valid. Long enough for
// someone to switch to their inbox and type six digits, short enough that
// a leaked/observed code is useless minutes later — same reasoning as
// oauthStateTTL in oauth_state_store.go.
const otpTTL = 10 * time.Minute

// maxOTPAttempts caps wrong guesses per issued code. Without this, a
// 6-digit code (1e6 space) is brute-forceable well within otpTTL by anyone
// who can hit the verify endpoint fast — the attempts counter, not the
// code space alone, is what makes this safe.
const maxOTPAttempts = 5

// OTPStore holds short-lived email-verification codes for the auth
// usecase (registration and password-reset — see
// internal/usecase/auth.UseCase's RequestRegistrationCode, Register,
// ForgotPassword, ResetPassword). Codes are namespaced by purpose+email
// (e.g. "register:someone@example.com" vs "password_reset:someone@example.com")
// so a code issued for one flow can't be replayed against the other, and
// so requesting a new code for one purpose doesn't invalidate an
// in-flight code for the other.
type OTPStore struct {
	client *redis.Client
}

func NewOTPStore(client *redis.Client) *OTPStore {
	return &OTPStore{client: client}
}

func codeKey(purpose, email string) string {
	return otpKeyPrefix + purpose + ":" + email
}

func attemptsKey(purpose, email string) string {
	return otpAttemptsKeyPrefix + purpose + ":" + email
}

// Save issues a new code for purpose+email, overwriting any code already
// pending for that purpose+email and resetting the attempt counter — a
// fresh "resend code" request should not be limited by attempts spent on
// a previous, now-discarded code.
func (s *OTPStore) Save(ctx context.Context, purpose, email, code string) error {
	if err := s.client.Set(ctx, codeKey(purpose, email), code, otpTTL).Err(); err != nil {
		return err
	}
	return s.client.Del(ctx, attemptsKey(purpose, email)).Err()
}

// Verify checks `code` against the pending code for purpose+email.
//
// On match: the code is consumed (deleted, single-use) and (true, nil) is
// returned.
//
// On mismatch: the attempt counter is incremented (TTL matched to the
// code's remaining life so it can't outlive it) and (false, nil) is
// returned — the caller should surface a generic "invalid or expired
// code" to the user either way, never distinguishing wrong-code from
// expired/unknown (that distinction is an enumeration/timing side channel).
//
// Once maxOTPAttempts is reached, the code is deleted outright (even if
// the *next* guess would have been correct) and (false, nil) is returned
// — the user must request a new code.
func (s *OTPStore) Verify(ctx context.Context, purpose, email, code string) (bool, error) {
	cKey := codeKey(purpose, email)
	aKey := attemptsKey(purpose, email)

	stored, err := s.client.Get(ctx, cKey).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	attempts, err := s.client.Incr(ctx, aKey).Result()
	if err != nil {
		return false, err
	}
	if attempts == 1 {
		// First attempt against this code — align the attempts key's TTL to
		// the code's so an abandoned attempts counter doesn't linger in
		// Redis past the code's own expiry.
		if ttl, ttlErr := s.client.TTL(ctx, cKey).Result(); ttlErr == nil && ttl > 0 {
			s.client.Expire(ctx, aKey, ttl)
		}
	}

	if attempts > maxOTPAttempts {
		s.client.Del(ctx, cKey, aKey)
		return false, nil
	}

	if stored != code {
		return false, nil
	}

	// Match — consume the code so it can't be replayed, and clear the
	// attempts counter.
	s.client.Del(ctx, cKey, aKey)
	return true, nil
}
