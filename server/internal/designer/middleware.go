package designer

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	authdesigner "boxland/server/internal/auth/designer"
)

type ctxKey struct{}

// CurrentDesigner reads the authenticated designer from the request
// context, or nil if the request is unauthenticated.
func CurrentDesigner(ctx context.Context) *authdesigner.Designer {
	d, _ := ctx.Value(ctxKey{}).(*authdesigner.Designer)
	return d
}

// LoadSession reads the designer session cookie (if present) and puts the
// matching Designer onto the request context. Invalid/expired cookies are
// silently dropped — downstream RequireDesigner enforces the auth check.
//
// This runs on every request to /design/* so any handler can call
// CurrentDesigner(r.Context()) without an extra DB lookup.
func LoadSession(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err == nil && cookie.Value != "" {
				designer, err := d.Auth.ValidateSession(r.Context(), cookie.Value)
				switch {
				case err == nil:
					r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, designer))
				case errors.Is(err, authdesigner.ErrSessionInvalid):
					// Best-effort cleanup: clear the bad cookie so we don't
					// keep validating it on every request.
					http.SetCookie(w, expiredSessionCookie())
				default:
					slog.Error("designer session validate", "err", err)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireDesigner is the gate for handlers that require a logged-in
// designer. Redirects unauthenticated browsers to /design/login;
// returns 401 for non-browser clients.
func RequireDesigner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if CurrentDesigner(r.Context()) == nil {
			if wantsHTML(r) {
				http.Redirect(w, r, "/design/login", http.StatusSeeOther)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func wantsHTML(r *http.Request) bool {
	for _, accept := range r.Header.Values("Accept") {
		if accept == "" {
			continue
		}
		if containsAny(accept, "text/html", "application/xhtml+xml") {
			return true
		}
	}
	// Default to HTML for browser-style requests if no header set.
	return r.Header.Get("HX-Request") == "" && r.Header.Get("Sec-Fetch-Mode") == "navigate"
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(s) >= len(n) {
			for i := 0; i+len(n) <= len(s); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}

func expiredSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}
