// Package designer implements the designer-realm auth surface:
// signup, login, logout, password reset, session validation, and the
// short-lived WebSocket ticket minting used to bridge the cookie session
// onto a WS connection (see PLAN.md §1 and §4b).
//
// All persistence goes through pgx + the package-level sql; tests against
// a real Postgres run alongside the rest of the test suite.
package designer

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
)

// Errors returned to callers. Stable for HTTP handler mapping.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrEmailInUse         = errors.New("auth: email already registered")
	ErrSessionInvalid     = errors.New("auth: session invalid or expired")
	ErrUnknownDesigner    = errors.New("auth: unknown designer")
)

// Role is a designer's permission tier. Values match the role CHECK
// constraint in 0004_designers.up.sql.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// Designer is a row from the designers table.
type Designer struct {
	ID           int64     `db:"id"`
	Email        string    `db:"email"`
	PasswordHash string    `db:"password_hash"`
	Role         Role      `db:"role"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}

// SessionTokenLen is the length in bytes of a session token before
// base64-encoding. 32 bytes = 256 bits of entropy.
const SessionTokenLen = 32

// SessionTTL is the cookie lifetime; reset rolling on each authenticated request.
const SessionTTL = 30 * 24 * time.Hour

// Service holds dependencies shared across handlers.
type Service struct {
	Pool *pgxpool.Pool
}

// New constructs a Service.
func New(pool *pgxpool.Pool) *Service { return &Service{Pool: pool} }

// ---- Signup / Login / Logout ----

// CreateDesigner inserts a designer with the given email + password.
// Returns ErrEmailInUse on conflict.
func (s *Service) CreateDesigner(ctx context.Context, email, password string, role Role) (*Designer, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	if role == "" {
		role = RoleEditor
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO designers (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id, email, password_hash, role, created_at, updated_at
	`, email, hash, string(role))
	var d Designer
	var roleStr string
	if err := row.Scan(&d.ID, &d.Email, &d.PasswordHash, &roleStr, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if isUniqueViolation(err, "designers_email_key") {
			return nil, ErrEmailInUse
		}
		return nil, fmt.Errorf("create designer: %w", err)
	}
	d.Role = Role(roleStr)
	return &d, nil
}

// FindByEmail looks up a designer by email (case-insensitive via citext).
func (s *Service) FindByEmail(ctx context.Context, email string) (*Designer, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	row := s.Pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, created_at, updated_at
		FROM designers WHERE email = $1
	`, email)
	var d Designer
	var roleStr string
	if err := row.Scan(&d.ID, &d.Email, &d.PasswordHash, &roleStr, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnknownDesigner
		}
		return nil, err
	}
	d.Role = Role(roleStr)
	return &d, nil
}

// HasAnyDesigner reports whether at least one designer row exists.
// Used by the first-signup-promotion path so the very first account
// becomes an owner instead of an editor.
func (s *Service) HasAnyDesigner(ctx context.Context) (bool, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM designers`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// FindByID looks up a designer by id.
func (s *Service) FindByID(ctx context.Context, id int64) (*Designer, error) {
	row := s.Pool.QueryRow(ctx, `
		SELECT id, email, password_hash, role, created_at, updated_at
		FROM designers WHERE id = $1
	`, id)
	var d Designer
	var roleStr string
	if err := row.Scan(&d.ID, &d.Email, &d.PasswordHash, &roleStr, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnknownDesigner
		}
		return nil, err
	}
	d.Role = Role(roleStr)
	return &d, nil
}

// Login verifies credentials and returns the designer on success.
// The caller is responsible for opening a session via OpenSession.
func (s *Service) Login(ctx context.Context, email, password string) (*Designer, error) {
	d, err := s.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUnknownDesigner) {
			// Generic message: don't disclose whether the email exists.
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := VerifyPassword(password, d.PasswordHash); err != nil {
		return nil, ErrInvalidCredentials
	}
	return d, nil
}

// ChangePassword updates a designer's password hash. Used by the
// password-reset flow once a reset token has been verified.
func (s *Service) ChangePassword(ctx context.Context, id int64, newPassword string) error {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx,
		`UPDATE designers SET password_hash = $2, updated_at = now() WHERE id = $1`,
		id, hash,
	)
	return err
}

// ---- Sessions ----

// Session is one authenticated cookie session.
type Session struct {
	TokenHash  []byte
	DesignerID int64
	CreatedAt  time.Time
	ExpiresAt  time.Time
	UserAgent  string
	IP         net.IP
}

// OpenSession mints a fresh session token. The raw token is returned (set on
// the cookie); only its sha256 is stored in designer_sessions.
func (s *Service) OpenSession(ctx context.Context, designerID int64, userAgent string, ip net.IP) (rawToken string, err error) {
	raw := make([]byte, SessionTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("rand token: %w", err)
	}
	rawToken = base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(rawToken))
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO designer_sessions (token_hash, designer_id, expires_at, user_agent, ip)
		VALUES ($1, $2, now() + $3::interval, $4, $5)
	`, hash[:], designerID, fmt.Sprintf("%d seconds", int(SessionTTL.Seconds())), userAgent, ipToAny(ip))
	if err != nil {
		return "", fmt.Errorf("open session: %w", err)
	}
	return rawToken, nil
}

// ValidateSession looks up a session by its raw cookie token. Returns the
// associated Designer and refreshes the session's expiry (rolling).
// Returns ErrSessionInvalid on missing/expired sessions.
func (s *Service) ValidateSession(ctx context.Context, rawToken string) (*Designer, error) {
	if rawToken == "" {
		return nil, ErrSessionInvalid
	}
	hash := sha256.Sum256([]byte(rawToken))

	var designerID int64
	var expiresAt time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT designer_id, expires_at FROM designer_sessions WHERE token_hash = $1
	`, hash[:]).Scan(&designerID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionInvalid
		}
		return nil, err
	}
	if time.Now().After(expiresAt) {
		_, _ = s.Pool.Exec(ctx, `DELETE FROM designer_sessions WHERE token_hash = $1`, hash[:])
		return nil, ErrSessionInvalid
	}
	// Roll the expiry forward.
	_, err = s.Pool.Exec(ctx, `
		UPDATE designer_sessions SET expires_at = now() + $2::interval WHERE token_hash = $1
	`, hash[:], fmt.Sprintf("%d seconds", int(SessionTTL.Seconds())))
	if err != nil {
		return nil, err
	}
	return s.FindByID(ctx, designerID)
}

// CloseSession deletes the session for the given raw token. No-op if missing.
func (s *Service) CloseSession(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(rawToken))
	_, err := s.Pool.Exec(ctx, `DELETE FROM designer_sessions WHERE token_hash = $1`, hash[:])
	return err
}

// PurgeExpiredSessions removes rows older than now(). Called periodically
// by a background job (wired in a later task); also safe to call during tests.
func (s *Service) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM designer_sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---- helpers ----

// ipToAny converts a net.IP to a value pgx can serialize into INET; nil maps
// to NULL. Avoids forcing callers to import pgtype.
func ipToAny(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}

// isUniqueViolation reports whether err is a Postgres unique_violation,
// optionally on a specific constraint. Uses pgconn.PgError directly so
// SQLState matching is reliable regardless of how the error is wrapped.
func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	if pe.Code != "23505" {
		return false
	}
	return constraint == "" || pe.ConstraintName == constraint
}
