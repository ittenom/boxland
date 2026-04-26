package designer_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/auth/designer"
	"boxland/server/internal/persistence/testdb"
)

// openTestPool returns an isolated, freshly-migrated DB. testdb.New wires its own t.Cleanup that drops the database when the test ends.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.New(t)
}

// resetDB is a no-op kept for call-site compatibility — testdb.New(t)
// already returns a fresh database for every test, so manual wipes
// are redundant. Future cleanup pass: drop the helper + every call.
func resetDB(t *testing.T, _ *pgxpool.Pool) {
	t.Helper()
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
