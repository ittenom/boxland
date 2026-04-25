package designer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
)

// WS ticket constants.
const (
	WSTicketLen = 32              // raw bytes before base64
	WSTicketTTL = 30 * time.Second
)

// ErrTicketInvalid is returned by RedeemWSTicket when the ticket is unknown,
// expired, already consumed, or bound to a different IP.
var ErrTicketInvalid = errors.New("auth: ws ticket invalid or expired")

// MintWSTicket creates a fresh one-shot WS ticket bound to the given
// designer and source IP. Returns the raw ticket string for the client to
// include in its FlatBuffers Auth message.
func (s *Service) MintWSTicket(ctx context.Context, designerID int64, ip net.IP) (string, error) {
	raw := make([]byte, WSTicketLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("rand ticket: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(ticket))

	if ip == nil {
		return "", errors.New("ws ticket: ip required")
	}

	_, err := s.Pool.Exec(ctx, `
		INSERT INTO designer_ws_tickets (ticket_hash, designer_id, ip, expires_at)
		VALUES ($1, $2, $3, now() + $4::interval)
	`, hash[:], designerID, ip.String(), fmt.Sprintf("%d seconds", int(WSTicketTTL.Seconds())))
	if err != nil {
		return "", fmt.Errorf("insert ws ticket: %w", err)
	}
	return ticket, nil
}

// RedeemWSTicket atomically consumes a WS ticket and returns the associated
// designer. The ticket must be unconsumed, unexpired, and (if expectedIP is
// non-nil) bound to that IP. After this returns, the ticket is permanently
// burnt.
func (s *Service) RedeemWSTicket(ctx context.Context, raw string, expectedIP net.IP) (*Designer, error) {
	if raw == "" {
		return nil, ErrTicketInvalid
	}
	hash := sha256.Sum256([]byte(raw))

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		designerID int64
		ipStr      string
		expiresAt  time.Time
		consumedAt *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT designer_id, host(ip), expires_at, consumed_at
		FROM designer_ws_tickets
		WHERE ticket_hash = $1
		FOR UPDATE
	`, hash[:]).Scan(&designerID, &ipStr, &expiresAt, &consumedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketInvalid
		}
		return nil, err
	}
	if consumedAt != nil || time.Now().After(expiresAt) {
		return nil, ErrTicketInvalid
	}
	if expectedIP != nil {
		if got := net.ParseIP(ipStr); got == nil || !got.Equal(expectedIP) {
			return nil, ErrTicketInvalid
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE designer_ws_tickets SET consumed_at = now() WHERE ticket_hash = $1`,
		hash[:],
	); err != nil {
		return nil, fmt.Errorf("consume ticket: %w", err)
	}

	d, err := s.findByIDInTx(ctx, tx, designerID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return d, nil
}

// PurgeExpiredWSTickets removes consumed or expired ticket rows.
// Safe to call periodically (cron-style background job; wired later).
func (s *Service) PurgeExpiredWSTickets(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM designer_ws_tickets
		WHERE consumed_at IS NOT NULL OR expires_at < now()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// findByIDInTx is FindByID restricted to a transaction. Avoids re-opening a
// pool connection mid-redeem.
func (s *Service) findByIDInTx(ctx context.Context, tx pgx.Tx, id int64) (*Designer, error) {
	var d Designer
	var roleStr string
	err := tx.QueryRow(ctx, `
		SELECT id, email, password_hash, role, created_at, updated_at
		FROM designers WHERE id = $1
	`, id).Scan(&d.ID, &d.Email, &d.PasswordHash, &roleStr, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnknownDesigner
		}
		return nil, err
	}
	d.Role = Role(roleStr)
	return &d, nil
}
