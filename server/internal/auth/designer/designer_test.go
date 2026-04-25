package designer_test

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/auth/designer"
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

// resetDB wipes designer tables both before the test runs and after, so
// tests are isolated regardless of run order.
func resetDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	wipe := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM designer_sessions`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM designers`)
	}
	wipe()
	t.Cleanup(wipe)
}

func TestArgon2_Roundtrip(t *testing.T) {
	hash, err := designer.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := designer.VerifyPassword("correct horse battery staple", hash); err != nil {
		t.Errorf("VerifyPassword(correct): %v", err)
	}
	if err := designer.VerifyPassword("wrong", hash); !errors.Is(err, designer.ErrInvalidCredentials) {
		t.Errorf("VerifyPassword(wrong): got %v, want ErrInvalidCredentials", err)
	}
}

func TestCreateAndLogin(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, err := svc.CreateDesigner(ctx, "Alice@example.com", "hunter2!", designer.RoleOwner)
	if err != nil {
		t.Fatalf("CreateDesigner: %v", err)
	}
	if d.Email != "alice@example.com" {
		t.Errorf("email should be normalized to lowercase, got %q", d.Email)
	}
	if d.Role != designer.RoleOwner {
		t.Errorf("role: got %q", d.Role)
	}

	got, err := svc.Login(ctx, "alice@example.com", "hunter2!")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got.ID != d.ID {
		t.Errorf("Login returned wrong designer: %+v", got)
	}
}

func TestCreateDuplicateEmail(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	if _, err := svc.CreateDesigner(ctx, "a@x.com", "p", designer.RoleEditor); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.CreateDesigner(ctx, "a@x.com", "p2", designer.RoleEditor)
	if !errors.Is(err, designer.ErrEmailInUse) {
		t.Errorf("got %v, want ErrEmailInUse", err)
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	_, _ = svc.CreateDesigner(ctx, "b@x.com", "right", designer.RoleEditor)
	_, err := svc.Login(ctx, "b@x.com", "wrong")
	if !errors.Is(err, designer.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUnknownEmailMasked(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)

	_, err := svc.Login(context.Background(), "ghost@x.com", "anything")
	if !errors.Is(err, designer.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials (mask unknown email)", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, _ := svc.CreateDesigner(ctx, "s@x.com", "p", designer.RoleEditor)
	tok, err := svc.OpenSession(ctx, d.ID, "ua/1.0", net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if tok == "" {
		t.Fatal("token should not be empty")
	}

	got, err := svc.ValidateSession(ctx, tok)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if got.ID != d.ID {
		t.Errorf("ValidateSession returned wrong designer")
	}

	if err := svc.CloseSession(ctx, tok); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if _, err := svc.ValidateSession(ctx, tok); !errors.Is(err, designer.ErrSessionInvalid) {
		t.Errorf("after close: got %v, want ErrSessionInvalid", err)
	}
}

func TestValidateSession_MissingTokenInvalid(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)

	if _, err := svc.ValidateSession(context.Background(), "garbage"); !errors.Is(err, designer.ErrSessionInvalid) {
		t.Errorf("got %v, want ErrSessionInvalid", err)
	}
	if _, err := svc.ValidateSession(context.Background(), ""); !errors.Is(err, designer.ErrSessionInvalid) {
		t.Errorf("empty token: got %v, want ErrSessionInvalid", err)
	}
}

func TestChangePassword(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, _ := svc.CreateDesigner(ctx, "c@x.com", "old", designer.RoleEditor)
	if err := svc.ChangePassword(ctx, d.ID, "new"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := svc.Login(ctx, "c@x.com", "old"); !errors.Is(err, designer.ErrInvalidCredentials) {
		t.Errorf("old password should fail")
	}
	if _, err := svc.Login(ctx, "c@x.com", "new"); err != nil {
		t.Errorf("new password should succeed: %v", err)
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	resetDB(t, pool)
	svc := designer.New(pool)
	ctx := context.Background()

	d, _ := svc.CreateDesigner(ctx, "p@x.com", "p", designer.RoleEditor)
	tok, _ := svc.OpenSession(ctx, d.ID, "", nil)

	// Manually expire it.
	_, err := pool.Exec(ctx,
		`UPDATE designer_sessions SET expires_at = now() - interval '1 hour' WHERE designer_id = $1`,
		d.ID)
	if err != nil {
		t.Fatalf("manual expire: %v", err)
	}

	n, err := svc.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 purge, got %d", n)
	}
	// Validation should now report invalid.
	if _, err := svc.ValidateSession(ctx, tok); !errors.Is(err, designer.ErrSessionInvalid) {
		t.Errorf("expired session should be invalid")
	}
}
