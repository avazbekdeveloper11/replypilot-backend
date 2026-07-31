package redis

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const oauthStateKeyPrefix = "oauth:ig:state:"

// oauthStateTTL bounds how long an in-flight OAuth handshake may take. Long
// enough for a human to grant permissions on Meta's screen, short enough
// that a leaked/observed state value is useless minutes later.
const oauthStateTTL = 10 * time.Minute

// OAuthStateStore holds the short-lived CSRF `state` values generated when a
// user starts the Instagram connect flow. The state is created server-side
// on connect and must be presented back (and match) on the callback —
// without this, an attacker could trick a logged-in user into completing a
// connect flow for an Instagram account the attacker controls (classic
// OAuth CSRF). Storing state server-side, keyed to the org that started the
// flow, is what makes the callback verifiable rather than trusting whatever
// state value the browser hands back.
type OAuthStateStore struct {
	client *redis.Client
}

func NewOAuthStateStore(client *redis.Client) *OAuthStateStore {
	return &OAuthStateStore{client: client}
}

// Save records that `state` was issued for orgID, with a TTL.
func (s *OAuthStateStore) Save(ctx context.Context, state string, orgID uuid.UUID) error {
	return s.client.Set(ctx, oauthStateKeyPrefix+state, orgID.String(), oauthStateTTL).Err()
}

// Consume atomically fetches and deletes the state, returning the org it was
// issued for. It is single-use by design (GetDel) — a state value can be
// redeemed exactly once, so a replayed callback fails. Returns ("", false)
// if the state is unknown, already used, or expired.
func (s *OAuthStateStore) Consume(ctx context.Context, state string) (uuid.UUID, bool, error) {
	val, err := s.client.GetDel(ctx, oauthStateKeyPrefix+state).Result()
	if err == redis.Nil {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	orgID, parseErr := uuid.Parse(val)
	if parseErr != nil {
		return uuid.Nil, false, parseErr
	}
	return orgID, true, nil
}
