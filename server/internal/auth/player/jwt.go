package player

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AccessClaims is the JWT body. Realm is "player" for every token issued
// by this package; the WS gateway uses it to assert the connection is
// player-realm before dispatching player verbs (PLAN.md §1).
type AccessClaims struct {
	Realm    string `json:"realm"`     // always "player"
	PlayerID int64  `json:"player_id"`
	jwt.RegisteredClaims
}

// MintAccessToken returns a signed HS256 access JWT for the player.
func (s *Service) MintAccessToken(p *Player) (string, error) {
	if p == nil {
		return "", errors.New("mint access token: nil player")
	}
	if len(s.JWTSecret) == 0 {
		return "", errors.New("mint access token: JWT secret unset")
	}
	now := time.Now()
	claims := AccessClaims{
		Realm:    "player",
		PlayerID: p.ID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Subject:   fmt.Sprintf("%d", p.ID),
			Issuer:    "boxland",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(s.JWTSecret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// ParseAccessToken verifies a signed access JWT and returns its claims.
// Errors fold into ErrTokenInvalid so callers can map all failure modes
// to a single response status.
func (s *Service) ParseAccessToken(raw string) (*AccessClaims, error) {
	if raw == "" {
		return nil, ErrTokenInvalid
	}
	if len(s.JWTSecret) == 0 {
		return nil, errors.New("parse access token: JWT secret unset")
	}
	claims := &AccessClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected alg %q", t.Method.Alg())
		}
		return s.JWTSecret, nil
	})
	if err != nil {
		return nil, ErrTokenInvalid
	}
	if !tok.Valid {
		return nil, ErrTokenInvalid
	}
	if claims.Realm != "player" {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}
