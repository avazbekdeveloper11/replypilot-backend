// Package jwtutil issues and verifies the two JWTs the API uses: short-lived
// access tokens (validated stateless, on every request, by middleware) and
// longer-lived refresh tokens (validated stateless AND checked against a
// Redis allowlist so they can be revoked on logout — see
// internal/usecase/auth for the revocation side).
package jwtutil

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
)

type Claims struct {
	UserID          uuid.UUID `json:"user_id"`
	OrganizationID  uuid.UUID `json:"organization_id"`
	RoleID          uuid.UUID `json:"role_id"`
	// IsPlatformAdmin marks a ReplyPilot staff account — unrelated to
	// RoleID, which is a per-organization team role (owner/admin/agent/
	// viewer). Re-derived from entity.User.IsPlatformAdmin at token-issue
	// time (Login, Register, Refresh), never trusted from stale claims
	// alone beyond a token's own TTL.
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	TokenType       TokenType `json:"token_type"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret          []byte
	issuer          string
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

func NewManager(secret, issuer string, accessTTL, refreshTTL time.Duration) *Manager {
	return &Manager{
		secret:          []byte(secret),
		issuer:          issuer,
		accessTokenTTL:  accessTTL,
		refreshTokenTTL: refreshTTL,
	}
}

func (m *Manager) RefreshTokenTTL() time.Duration { return m.refreshTokenTTL }

// GeneratedToken carries both the signed JWT and its jti (JWT ID), the
// latter used as the Redis key when the refresh token needs to be
// allowlisted or revoked.
type GeneratedToken struct {
	Token     string
	JTI       string
	ExpiresAt time.Time
}

func (m *Manager) GenerateAccessToken(userID, orgID, roleID uuid.UUID, isPlatformAdmin bool) (*GeneratedToken, error) {
	return m.generate(userID, orgID, roleID, isPlatformAdmin, AccessToken, m.accessTokenTTL)
}

func (m *Manager) GenerateRefreshToken(userID, orgID, roleID uuid.UUID, isPlatformAdmin bool) (*GeneratedToken, error) {
	return m.generate(userID, orgID, roleID, isPlatformAdmin, RefreshToken, m.refreshTokenTTL)
}

func (m *Manager) generate(userID, orgID, roleID uuid.UUID, isPlatformAdmin bool, tokenType TokenType, ttl time.Duration) (*GeneratedToken, error) {
	jti := uuid.NewString()
	now := time.Now()
	expiresAt := now.Add(ttl)

	claims := Claims{
		UserID:          userID,
		OrganizationID:  orgID,
		RoleID:          roleID,
		IsPlatformAdmin: isPlatformAdmin,
		TokenType:       tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return nil, err
	}

	return &GeneratedToken{Token: signed, JTI: jti, ExpiresAt: expiresAt}, nil
}

var (
	ErrInvalidToken = errors.New("jwtutil: invalid token")
	ErrExpiredToken = errors.New("jwtutil: token expired")
	ErrWrongType    = errors.New("jwtutil: unexpected token type")
)

// Parse validates signature and expiry and asserts the token is of
// expectedType — an access token presented where a refresh token is
// expected (or vice versa) is rejected here, not left to caller discipline.
func (m *Manager) Parse(tokenString string, expectedType TokenType) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	if !token.Valid {
		return nil, ErrInvalidToken
	}

	if claims.TokenType != expectedType {
		return nil, ErrWrongType
	}

	return claims, nil
}
