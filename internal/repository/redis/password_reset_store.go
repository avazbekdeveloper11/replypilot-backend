package redis

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const passwordResetKeyPrefix = "auth:password-reset:"

// passwordResetTTL bounds how long a reset link is valid. Long enough for
// someone to find the link (especially given the log-based notifier — see
// internal/platform/notify — until a real email provider is wired up),
// short enough that an old, forgotten link isn't a standing risk.
const passwordResetTTL = 30 * time.Minute

// PasswordResetStore holds the single-use token issued when a user starts
// the forgot-password flow, keyed to the user it was issued for. Same
// shape and same reasoning as OAuthStateStore: the token is opaque to the
// client, stored server-side, and redeemable exactly once.
type PasswordResetStore struct {
	client *redis.Client
}

func NewPasswordResetStore(client *redis.Client) *PasswordResetStore {
	return &PasswordResetStore{client: client}
}

// Save records that `token` was issued for userID, with a TTL.
func (s *PasswordResetStore) Save(ctx context.Context, token string, userID uuid.UUID) error {
	return s.client.Set(ctx, passwordResetKeyPrefix+token, userID.String(), passwordResetTTL).Err()
}

// Consume atomically fetches and deletes the token, returning the user it
// was issued for. Single-use by design (GetDel) — redeeming a reset token
// twice (e.g. a replayed request, or an email client "clicking" the link
// via link-preview prefetching) must not silently succeed twice. Returns
// (uuid.Nil, false) if the token is unknown, already used, or expired.
func (s *PasswordResetStore) Consume(ctx context.Context, token string) (uuid.UUID, bool, error) {
	val, err := s.client.GetDel(ctx, passwordResetKeyPrefix+token).Result()
	if err == redis.Nil {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	userID, parseErr := uuid.Parse(val)
	if parseErr != nil {
		return uuid.Nil, false, parseErr
	}
	return userID, true, nil
}
