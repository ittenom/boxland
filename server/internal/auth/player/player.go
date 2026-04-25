// Package player implements the player-realm auth surface: email/password
// signup, email verification, JWT minting, refresh tokens, and OAuth link
// management.
//
// PLAN.md §1 / §10: player and designer realms are STRICTLY separated.
// Player JWTs cannot authenticate WS connections as designers and vice
// versa; the gateway tags the connection from the Auth handshake and
// dispatch checks the realm tag, never the token claims.
package player

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	authdesigner "boxland/server/internal/auth/designer"
)

// Errors returned to callers. Stable for HTTP handler mapping.
var (
	ErrInvalidCredentials = errors.New("player: invalid credentials")
	ErrEmailInUse         = errors.New("player: email already registered")
	ErrUnknownPlayer      = errors.New("player: unknown player")
	ErrEmailNotVerified   = errors.New("player: email not verified")
	ErrTokenInvalid       = errors.New("player: token invalid or expired")
	ErrSessionInvalid     = errors.New("player: refresh session invalid or expired")
)

// Player is one row from the players table.
type Player struct {
	ID            int64     `db:"id"             json:"id"`
	Email         string    `db:"email"          json:"email"`
	PasswordHash  *string   `db:"password_hash"  json:"-"`
	EmailVerified bool      `db:"email_verified" json:"email_verified"`
	DisplayName   *string   `db:"display_name"   json:"display_name,omitempty"`
	CreatedAt     time.Time `db:"created_at"     json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"     json:"updated_at"`
}

// Token TTLs. Access JWTs are intentionally short; the WS connection
// reauths via the refresh token if needed.
const (
	AccessTokenTTL          = 15 * time.Minute
	RefreshTokenTTL         = 30 * 24 * time.Hour
	EmailVerificationTTL    = 24 * time.Hour
	RefreshTokenLen         = 32 // raw bytes before base64
	EmailVerificationLen    = 32
)

// Service holds the dependencies. The designer auth package's argon2
// helpers are reused here so we have one canonical password-hashing
// implementation across realms.
type Service struct {
	Pool          *pgxpool.Pool
	JWTSecret     []byte // signing key for HS256 access tokens
}

// New constructs a Service.
func New(pool *pgxpool.Pool, jwtSecret []byte) *Service {
	return &Service{Pool: pool, JWTSecret: jwtSecret}
}

// ---- Signup ----

// CreatePlayer inserts a player. Returns ErrEmailInUse on conflict. The
// returned Player has email_verified=false; the caller should mint an
// EmailVerification + send the email out-of-band.
func (s *Service) CreatePlayer(ctx context.Context, email, password string) (*Player, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	hash, err := authdesigner.HashPassword(password)
	if err != nil {
		return nil, err
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO players (email, password_hash, email_verified)
		VALUES ($1, $2, false)
		RETURNING id, email, password_hash, email_verified, display_name, created_at, updated_at
	`, email, hash)
	p, err := scanPlayer(row)
	if err != nil {
		if isUniqueViolation(err, "players_email_key") {
			return nil, ErrEmailInUse
		}
		return nil, fmt.Errorf("create player: %w", err)
	}
	return p, nil
}

// FindByEmail looks up a player.
func (s *Service) FindByEmail(ctx context.Context, email string) (*Player, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, email, password_hash, email_verified, display_name, created_at, updated_at
		FROM players WHERE email = $1
	`, strings.ToLower(strings.TrimSpace(email)))
	p, err := scanPlayer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnknownPlayer
		}
		return nil, err
	}
	return p, nil
}

// FindByID looks up a player.
func (s *Service) FindByID(ctx context.Context, id int64) (*Player, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, email, password_hash, email_verified, display_name, created_at, updated_at
		FROM players WHERE id = $1
	`, id)
	p, err := scanPlayer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnknownPlayer
		}
		return nil, err
	}
	return p, nil
}

// Login verifies credentials and returns the player on success. Does NOT
// open a session by itself -- callers chain MintAccessToken + OpenRefreshSession.
func (s *Service) Login(ctx context.Context, email, password string) (*Player, error) {
	p, err := s.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUnknownPlayer) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if p.PasswordHash == nil {
		return nil, ErrInvalidCredentials // OAuth-only account
	}
	if err := authdesigner.VerifyPassword(password, *p.PasswordHash); err != nil {
		return nil, ErrInvalidCredentials
	}
	if !p.EmailVerified {
		return nil, ErrEmailNotVerified
	}
	return p, nil
}

// ---- Email verification ----

// IssueEmailVerification mints a one-shot token bound to the player. The
// caller is responsible for sending the email containing the raw token;
// only its sha256 is stored.
func (s *Service) IssueEmailVerification(ctx context.Context, playerID int64) (string, error) {
	raw := make([]byte, EmailVerificationLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("rand verification: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tok))
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO player_email_verifications (token_hash, player_id, expires_at)
		VALUES ($1, $2, now() + $3::interval)
	`, hash[:], playerID, fmt.Sprintf("%d seconds", int(EmailVerificationTTL.Seconds()))); err != nil {
		return "", fmt.Errorf("insert verification: %w", err)
	}
	return tok, nil
}

// VerifyEmail consumes a verification token and marks the player's email
// verified. Idempotent: a second call with the same token returns
// ErrTokenInvalid.
func (s *Service) VerifyEmail(ctx context.Context, rawToken string) (*Player, error) {
	if rawToken == "" {
		return nil, ErrTokenInvalid
	}
	hash := sha256.Sum256([]byte(rawToken))

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		playerID   int64
		expiresAt  time.Time
		consumedAt *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT player_id, expires_at, consumed_at FROM player_email_verifications
		WHERE token_hash = $1
		FOR UPDATE
	`, hash[:]).Scan(&playerID, &expiresAt, &consumedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTokenInvalid
		}
		return nil, err
	}
	if consumedAt != nil || time.Now().After(expiresAt) {
		return nil, ErrTokenInvalid
	}

	if _, err := tx.Exec(ctx,
		`UPDATE player_email_verifications SET consumed_at = now() WHERE token_hash = $1`,
		hash[:]); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE players SET email_verified = true, updated_at = now() WHERE id = $1`,
		playerID); err != nil {
		return nil, err
	}

	row := tx.QueryRow(ctx, `
		SELECT id, email, password_hash, email_verified, display_name, created_at, updated_at
		FROM players WHERE id = $1
	`, playerID)
	p, err := scanPlayer(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// ---- Refresh sessions ----

// OpenRefreshSession mints a long-lived refresh token. Returns the raw
// token; only the sha256 is stored.
func (s *Service) OpenRefreshSession(ctx context.Context, playerID int64, ua string, ip net.IP) (string, error) {
	raw := make([]byte, RefreshTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(tok))
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO player_sessions (refresh_token_hash, player_id, expires_at, user_agent, ip)
		VALUES ($1, $2, now() + $3::interval, $4, $5)
	`, hash[:], playerID, fmt.Sprintf("%d seconds", int(RefreshTokenTTL.Seconds())), ua, ipToAny(ip))
	if err != nil {
		return "", err
	}
	return tok, nil
}

// ValidateRefreshSession checks a refresh token and returns the
// associated player. Does NOT rotate the token; the caller decides
// whether to re-issue.
func (s *Service) ValidateRefreshSession(ctx context.Context, raw string) (*Player, error) {
	if raw == "" {
		return nil, ErrSessionInvalid
	}
	hash := sha256.Sum256([]byte(raw))
	var playerID int64
	var expiresAt time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT player_id, expires_at FROM player_sessions WHERE refresh_token_hash = $1
	`, hash[:]).Scan(&playerID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionInvalid
		}
		return nil, err
	}
	if time.Now().After(expiresAt) {
		_, _ = s.Pool.Exec(ctx,
			`DELETE FROM player_sessions WHERE refresh_token_hash = $1`, hash[:])
		return nil, ErrSessionInvalid
	}
	return s.FindByID(ctx, playerID)
}

// CloseRefreshSession deletes the refresh token. Idempotent.
func (s *Service) CloseRefreshSession(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(raw))
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM player_sessions WHERE refresh_token_hash = $1`, hash[:])
	return err
}

// ---- helpers ----

func scanPlayer(row pgx.Row) (*Player, error) {
	var p Player
	if err := row.Scan(&p.ID, &p.Email, &p.PasswordHash, &p.EmailVerified, &p.DisplayName, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func ipToAny(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}

func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
