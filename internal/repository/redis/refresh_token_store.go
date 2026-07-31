// Package redis implements the (small number of) repository ports that are
// naturally Redis-backed rather than Postgres-backed — refresh token
// revocation here, and the rate limiter's counters live directly in the
// middleware since they're not a domain repository.
package redis

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const refreshTokenKeyPrefix = "auth:refresh:"

// RefreshTokenStore allowlists issued refresh-token JTIs in Redis with a
// TTL matching the token's own expiry. A JWT's signature proves it was
// issued by us and hasn't been tampered with, but says nothing about
// whether it's since been revoked (logout, password change, admin action)
// — this store is what makes revocation possible at all for an otherwise
// stateless token.
type RefreshTokenStore struct {
	client *redis.Client
}

func NewRefreshTokenStore(client *redis.Client) *RefreshTokenStore {
	return &RefreshTokenStore{client: client}
}

func (s *RefreshTokenStore) Store(ctx context.Context, jti string, userID uuid.UUID, ttl time.Duration) error {
	return s.client.Set(ctx, refreshTokenKeyPrefix+jti, userID.String(), ttl).Err()
}

func (s *RefreshTokenStore) Exists(ctx context.Context, jti string) (bool, error) {
	n, err := s.client.Exists(ctx, refreshTokenKeyPrefix+jti).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *RefreshTokenStore) Revoke(ctx context.Context, jti string) error {
	return s.client.Del(ctx, refreshTokenKeyPrefix+jti).Err()
}
