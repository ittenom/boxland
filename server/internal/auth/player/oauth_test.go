package player_test

import (
	"context"
	"strings"
	"testing"

	"boxland/server/internal/auth/player"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := player.NewRegistry()
	r.Register(player.NewGoogleProvider(player.GoogleConfig{
		ClientID: "id", ClientSecret: "secret", RedirectURL: "https://example/cb",
	}))
	if _, ok := r.Get("google"); !ok {
		t.Errorf("google should be registered")
	}
	if _, ok := r.Get("ghost"); ok {
		t.Errorf("unknown providers should not match")
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := player.NewRegistry()
	g := player.NewGoogleProvider(player.GoogleConfig{ClientID: "x", ClientSecret: "y", RedirectURL: "z"})
	r.Register(g)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate provider")
		}
	}()
	r.Register(g)
}

func TestNewGoogleProvider_NilOnEmptyConfig(t *testing.T) {
	if g := player.NewGoogleProvider(player.GoogleConfig{}); g != nil {
		t.Errorf("empty config should yield nil provider; got %v", g)
	}
}

func TestNewOAuthState_Different(t *testing.T) {
	a, _ := player.NewOAuthState()
	b, _ := player.NewOAuthState()
	if a == "" || b == "" || a == b {
		t.Errorf("states should be non-empty and distinct: %q vs %q", a, b)
	}
}

func TestNewPKCEVerifier_LengthOK(t *testing.T) {
	v, err := player.NewPKCEVerifier()
	if err != nil {
		t.Fatal(err)
	}
	// RFC 7636: 43..128 chars after base64url encoding.
	if len(v) < 43 || len(v) > 128 {
		t.Errorf("verifier length %d out of RFC 7636 range", len(v))
	}
}

func TestLinkOrCreatePlayer_NewPlayerWithVerifiedEmail(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc := player.New(pool, []byte(testJWTSecret))

	p, err := svc.LinkOrCreatePlayer(context.Background(),
		"google", "google-uid-1", "fresh@example.com", true)
	if err != nil {
		t.Fatalf("LinkOrCreatePlayer: %v", err)
	}
	if p.Email != "fresh@example.com" {
		t.Errorf("email: got %q", p.Email)
	}
	if !p.EmailVerified {
		t.Error("verified-email IdP signin should mark verified")
	}
	if p.PasswordHash != nil {
		t.Error("OAuth-only account should have no password hash")
	}
}

func TestLinkOrCreatePlayer_LinksToExistingEmail(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	original, _ := svc.CreatePlayer(ctx, "shared@x.com", "p")
	tok, _ := svc.IssueEmailVerification(ctx, original.ID)
	_, _ = svc.VerifyEmail(ctx, tok)

	got, err := svc.LinkOrCreatePlayer(ctx, "google", "google-uid-2", "shared@x.com", true)
	if err != nil {
		t.Fatalf("LinkOrCreatePlayer: %v", err)
	}
	if got.ID != original.ID {
		t.Errorf("expected link to existing player; got new id %d (was %d)", got.ID, original.ID)
	}
}

func TestLinkOrCreatePlayer_SubsequentSignInReusesLink(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc := player.New(pool, []byte(testJWTSecret))
	ctx := context.Background()

	first, err := svc.LinkOrCreatePlayer(ctx, "google", "uid-3", "first@x.com", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.LinkOrCreatePlayer(ctx, "google", "uid-3", "first@x.com", true)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("repeat sign-in should reuse player; %d vs %d", first.ID, second.ID)
	}
}

func TestLinkOrCreatePlayer_RejectsEmptyProviderUserID(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	svc := player.New(pool, []byte(testJWTSecret))

	_, err := svc.LinkOrCreatePlayer(context.Background(), "google", "", "x@y.com", true)
	if err == nil || !strings.Contains(err.Error(), "provider_user_id") {
		t.Errorf("expected provider_user_id error; got %v", err)
	}
}
