package designer_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"boxland/server/internal/auth/designer"
)

func TestWSTicket_RoundtripBindsToIP(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, _ := svc.CreateDesigner(ctx, "wsa@x.com", "p", designer.RoleEditor)

	ip := net.ParseIP("203.0.113.5")
	tok, err := svc.MintWSTicket(ctx, d.ID, ip)
	if err != nil {
		t.Fatalf("MintWSTicket: %v", err)
	}

	got, err := svc.RedeemWSTicket(ctx, tok, ip)
	if err != nil {
		t.Fatalf("RedeemWSTicket: %v", err)
	}
	if got.ID != d.ID {
		t.Errorf("redeemed wrong designer")
	}

	// Second redeem should fail (one-shot).
	if _, err := svc.RedeemWSTicket(ctx, tok, ip); !errors.Is(err, designer.ErrTicketInvalid) {
		t.Errorf("second redeem: got %v, want ErrTicketInvalid", err)
	}
}

func TestWSTicket_WrongIPRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, _ := svc.CreateDesigner(ctx, "wsb@x.com", "p", designer.RoleEditor)
	tok, _ := svc.MintWSTicket(ctx, d.ID, net.ParseIP("203.0.113.5"))

	_, err := svc.RedeemWSTicket(ctx, tok, net.ParseIP("203.0.113.99"))
	if !errors.Is(err, designer.ErrTicketInvalid) {
		t.Errorf("got %v, want ErrTicketInvalid", err)
	}
}

func TestWSTicket_ExpiredRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, _ := svc.CreateDesigner(ctx, "wsc@x.com", "p", designer.RoleEditor)
	tok, _ := svc.MintWSTicket(ctx, d.ID, net.ParseIP("127.0.0.1"))

	// Force-expire it.
	_, err := pool.Exec(ctx,
		`UPDATE designer_ws_tickets SET expires_at = now() - interval '1 minute' WHERE designer_id = $1`,
		d.ID)
	if err != nil {
		t.Fatalf("force expire: %v", err)
	}

	_, err = svc.RedeemWSTicket(ctx, tok, net.ParseIP("127.0.0.1"))
	if !errors.Is(err, designer.ErrTicketInvalid) {
		t.Errorf("got %v, want ErrTicketInvalid", err)
	}
}

func TestWSTicket_PurgeExpired(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, _ := svc.CreateDesigner(ctx, "wsd@x.com", "p", designer.RoleEditor)

	// One unconsumed-but-expired, one consumed, one fresh.
	_, _ = svc.MintWSTicket(ctx, d.ID, net.ParseIP("127.0.0.1"))
	tok2, _ := svc.MintWSTicket(ctx, d.ID, net.ParseIP("127.0.0.1"))
	_, _ = svc.RedeemWSTicket(ctx, tok2, net.ParseIP("127.0.0.1"))
	_, _ = svc.MintWSTicket(ctx, d.ID, net.ParseIP("127.0.0.1"))

	// Force-expire the first ticket.
	_, err := pool.Exec(ctx, `
		UPDATE designer_ws_tickets
		SET expires_at = now() - interval '1 minute'
		WHERE consumed_at IS NULL
		AND ticket_hash IN (
			SELECT ticket_hash FROM designer_ws_tickets WHERE consumed_at IS NULL ORDER BY expires_at ASC LIMIT 1
		)
	`)
	if err != nil {
		t.Fatalf("force expire: %v", err)
	}

	n, err := svc.PurgeExpiredWSTickets(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredWSTickets: %v", err)
	}
	if n != 2 {
		t.Errorf("expected purge of 2 (1 consumed, 1 expired), got %d", n)
	}

	// Allow some clock drift safety margin.
	_ = time.Now()
}
