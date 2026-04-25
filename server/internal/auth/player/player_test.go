package player_test

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/auth/player"
	"boxland/server/internal/persistence/testdb"
)

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://boxland:boxland_dev@localhost:5433/boxland?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("postgres unavailable: %v", err)
	}
	return pool
}

const testJWTSecret = "test-secret-key-do-not-use-in-prod"

func TestCreatePlayer_DefaultsToUnverified(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))

	p, err := svc.CreatePlayer(context.Background(), "Player@x.com", "hunter2!")
	if err != nil {
		t.Fatalf("CreatePlayer: %v", err)
	}
	if p.Email != "player@x.com" {
		t.Errorf("email should be lowercased; got %q", p.Email)
	}
	if p.EmailVerified {
		t.Error("freshly created player should be unverified")
	}
}

func TestCreatePlayer_DuplicateEmailRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))

	_, _ = svc.CreatePlayer(context.Background(), "dup@x.com", "p")
	_, err := svc.CreatePlayer(context.Background(), "dup@x.com", "p2")
	if !errors.Is(err, player.ErrEmailInUse) {
		t.Errorf("got %v, want ErrEmailInUse", err)
	}
}

func TestLogin_RejectsUnverifiedEmail(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	_, _ = svc.CreatePlayer(ctx, "unv@x.com", "p")
	_, err := svc.Login(ctx, "unv@x.com", "p")
	if !errors.Is(err, player.ErrEmailNotVerified) {
		t.Errorf("got %v, want ErrEmailNotVerified", err)
	}
}

func TestEmailVerification_HappyPath(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	p, _ := svc.CreatePlayer(ctx, "v@x.com", "p")
	tok, err := svc.IssueEmailVerification(ctx, p.ID)
	if err != nil {
		t.Fatalf("IssueEmailVerification: %v", err)
	}
	verified, err := svc.VerifyEmail(ctx, tok)
	if err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	if !verified.EmailVerified {
		t.Error("VerifyEmail should mark the row verified")
	}
	// Now Login should succeed.
	got, err := svc.Login(ctx, "v@x.com", "p")
	if err != nil {
		t.Fatalf("Login after verify: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("Login returned wrong player")
	}
}

func TestEmailVerification_DoubleConsumeRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	p, _ := svc.CreatePlayer(ctx, "double@x.com", "p")
	tok, _ := svc.IssueEmailVerification(ctx, p.ID)
	_, err := svc.VerifyEmail(ctx, tok)
	if err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := svc.VerifyEmail(ctx, tok); !errors.Is(err, player.ErrTokenInvalid) {
		t.Errorf("second verify: got %v, want ErrTokenInvalid", err)
	}
}

func TestEmailVerification_ExpiredRejected(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	p, _ := svc.CreatePlayer(ctx, "exp@x.com", "p")
	tok, _ := svc.IssueEmailVerification(ctx, p.ID)
	// Manually expire it.
	if _, err := pool.Exec(ctx,
		`UPDATE player_email_verifications SET expires_at = now() - interval '1 hour' WHERE player_id = $1`,
		p.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyEmail(ctx, tok); !errors.Is(err, player.ErrTokenInvalid) {
		t.Errorf("got %v, want ErrTokenInvalid", err)
	}
}

func TestRefreshSession_Lifecycle(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	p, _ := svc.CreatePlayer(ctx, "rs@x.com", "p")
	tok, err := svc.OpenRefreshSession(ctx, p.ID, "ua/test", net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatalf("OpenRefreshSession: %v", err)
	}
	if tok == "" {
		t.Fatal("token should not be empty")
	}

	got, err := svc.ValidateRefreshSession(ctx, tok)
	if err != nil {
		t.Fatalf("ValidateRefreshSession: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("validate returned wrong player")
	}

	if err := svc.CloseRefreshSession(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ValidateRefreshSession(ctx, tok); !errors.Is(err, player.ErrSessionInvalid) {
		t.Errorf("post-close validate: got %v, want ErrSessionInvalid", err)
	}
}

func TestJWT_Roundtrip(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	p, _ := svc.CreatePlayer(ctx, "jwt@x.com", "p")
	tok, err := svc.MintAccessToken(p)
	if err != nil {
		t.Fatalf("MintAccessToken: %v", err)
	}
	claims, err := svc.ParseAccessToken(tok)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if claims.PlayerID != p.ID {
		t.Errorf("PlayerID: got %d, want %d", claims.PlayerID, p.ID)
	}
	if claims.Realm != "player" {
		t.Errorf("Realm: got %q, want player", claims.Realm)
	}
}

func TestJWT_RejectsUnknownToken(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc := player.New(pool, []byte(testJWTSecret))

	if _, err := svc.ParseAccessToken("not-a-jwt"); !errors.Is(err, player.ErrTokenInvalid) {
		t.Errorf("got %v, want ErrTokenInvalid", err)
	}
	if _, err := svc.ParseAccessToken(""); !errors.Is(err, player.ErrTokenInvalid) {
		t.Errorf("empty: got %v, want ErrTokenInvalid", err)
	}
}

func TestJWT_RejectsTokenSignedWithDifferentKey(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	testdb.Reset(t, pool)
	a := player.New(pool, []byte("key-a"))
	b := player.New(pool, []byte("key-b"))
	ctx := context.Background()

	p, _ := a.CreatePlayer(ctx, "diff@x.com", "p")
	tok, _ := a.MintAccessToken(p)
	if _, err := b.ParseAccessToken(tok); !errors.Is(err, player.ErrTokenInvalid) {
		t.Errorf("got %v, want ErrTokenInvalid", err)
	}
}
