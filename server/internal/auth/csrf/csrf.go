// Package csrf implements double-submit-cookie CSRF protection for the
// design-tool surfaces.
//
// On every response a CSRF cookie is set if missing. The Templ shell mirrors
// the cookie value into a <meta name="csrf-token"> tag; web/static/js wires
// HTMX (htmx:configRequest) to copy the meta value into an X-CSRF-Token
// header on every state-changing request. This middleware verifies that
// the submitted token equals the cookie for unsafe methods (POST/PUT/
// PATCH/DELETE).
//
// Two ways to submit the token are accepted, in order:
//  1. X-CSRF-Token request header — used by HTMX/fetch.
//  2. csrf_token form field — used by plain <form method="post">, which
//     cannot set custom headers. Only consulted for form-encoded bodies
//     (application/x-www-form-urlencoded, multipart/form-data) so JSON
//     handlers that re-parse the body downstream aren't affected.
//
// Safe methods (GET/HEAD/OPTIONS) are passed through unchecked so links and
// HTMX hx-get calls don't need the header. WebSocket upgrades go through a
// separate flow (designer WS ticket; see PLAN.md §1).
package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

const (
	CookieName = "boxland_csrf"
	HeaderName = "X-CSRF-Token"
	// FormField is the hidden-input name for plain HTML form submissions.
	// The views.CSRFInput() Templ helper emits this for every plain form.
	FormField  = "csrf_token"
	tokenBytes = 32 // 256 bits
)

type ctxKey struct{}

// Token reads the active CSRF token from the request context. Empty if
// the middleware hasn't run yet.
func Token(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// IsSafeMethod returns true for HTTP methods that do not need CSRF
// verification (and need no cookie write either).
func IsSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// Config tunes the cookie attributes. Defaults are production-safe.
type Config struct {
	CookieDomain string // "" = current host
	Secure       bool   // true in prod (HTTPS); usually false in dev
	SameSite     http.SameSite
}

// DefaultConfig returns dev-friendly defaults: SameSite=Lax, Secure=false.
// Production should set Secure=true.
func DefaultConfig() Config {
	return Config{SameSite: http.SameSiteLaxMode}
}

// Middleware enforces double-submit-cookie CSRF. Place it after session
// middleware (so the auth context is already populated) but before route
// handlers.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, err := getOrSetToken(w, r, cfg)
			if err != nil {
				http.Error(w, "csrf: token issue", http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, tok)
			r = r.WithContext(ctx)

			if IsSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			submitted := r.Header.Get(HeaderName)
			if submitted == "" && isFormBody(r) {
				// PostFormValue parses the body into r.PostForm. That's
				// fine for form-encoded handlers (they read from
				// r.PostForm / r.FormValue too), but we don't want to
				// touch JSON bodies — handlers like ws-ticket / settings
				// re-decode the raw stream.
				submitted = r.PostFormValue(FormField)
			}
			if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(tok)) != 1 {
				http.Error(w, "csrf: token mismatch", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isFormBody reports whether the request body is form-encoded (and so safe
// to feed to PostFormValue without disrupting downstream parsing). Anything
// else — JSON, octet-stream, FlatBuffers, etc. — is left untouched.
func isFormBody(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	// Strip any "; charset=..." / "; boundary=..." parameters.
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	return ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data"
}

// getOrSetToken returns the CSRF token from the request cookie, generating
// and setting one if absent.
func getOrSetToken(w http.ResponseWriter, r *http.Request, cfg Config) (string, error) {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value, nil
	}
	tok, err := generateToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    tok,
		Path:     "/",
		Domain:   cfg.CookieDomain,
		Secure:   cfg.Secure,
		HttpOnly: false, // must be readable by JS so HTMX can mirror it
		SameSite: cfg.SameSite,
		MaxAge:   60 * 60 * 24 * 7, // 7d; rotated by clearing the cookie
	})
	return tok, nil
}

func generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ErrMissingToken is returned by Verify when no token is present in context.
// Most callers should rely on the middleware; Verify is a low-level helper.
var ErrMissingToken = errors.New("csrf: token missing from context")
