package player

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GoogleConfig is the per-deployment configuration for the Google provider.
// Loaded from env in cmd/boxland/main.go (OAUTH_GOOGLE_CLIENT_ID etc).
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string // e.g. https://boxland.example/auth/oauth/google/callback
}

// GoogleProvider implements Provider against Google's OIDC endpoints.
type GoogleProvider struct {
	cfg *oauth2.Config
}

// NewGoogleProvider constructs the provider. Returns nil if cfg is empty
// (caller treats that as "feature flag off"). Deliberately constructive
// so the gateway can decide whether to register based on a single check.
func NewGoogleProvider(cfg GoogleConfig) *GoogleProvider {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil
	}
	return &GoogleProvider{
		cfg: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
	}
}

func (*GoogleProvider) Name() string { return "google" }

// AuthCodeURL returns the user-facing redirect URL with PKCE.
func (g *GoogleProvider) AuthCodeURL(state, pkceVerifier string) string {
	challenge := s256Challenge(pkceVerifier)
	return g.cfg.AuthCodeURL(state,
		oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// Exchange completes the auth code grant, fetches the userinfo endpoint,
// and returns the (provider_user_id, email) pair.
func (g *GoogleProvider) Exchange(ctx context.Context, code, pkceVerifier string) (string, string, error) {
	tok, err := g.cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", pkceVerifier))
	if err != nil {
		return "", "", fmt.Errorf("google exchange: %w", err)
	}

	cli := g.cfg.Client(ctx, tok)
	resp, err := cli.Get("https://openidconnect.googleapis.com/v1/userinfo")
	if err != nil {
		return "", "", fmt.Errorf("google userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("google userinfo: status %d: %s", resp.StatusCode, body)
	}

	var info struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", fmt.Errorf("decode userinfo: %w", err)
	}
	if info.Sub == "" {
		return "", "", fmt.Errorf("google userinfo missing sub")
	}
	return info.Sub, info.Email, nil
}

// s256Challenge computes the RFC 7636 S256 PKCE challenge from a verifier.
func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
