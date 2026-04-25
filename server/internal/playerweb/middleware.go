// Package playerweb wires the player-facing HTTP surface: signup, login,
// logout, server picker, map picker, and the game view that boots
// web/src/game/. All routes here run inside the player realm.
//
// PLAN.md §4n + §6h: player authentication uses the same JWT + refresh
// session machinery the WS gateway already trusts. The HTTP cookie
// holds a long-lived refresh token; the access JWT is minted on demand
// (one POST /play/ws-ticket call returns a fresh JWT for the WS Auth
// handshake).
package playerweb

import (
	"context"
	"errors"
	"net/http"

	"boxland/server/internal/auth/player"
)

// SessionCookieName is the cookie carrying the player refresh token.
// Distinct from the designer cookie so a single browser can hold both
// realms simultaneously without one clobbering the other.
const SessionCookieName = "boxland_player"

// playerCtxKey scopes the request-context entry that LoadSession
// populates. Unexported so only this package can read it; handlers use
// PlayerFromContext.
type playerCtxKey struct{}

// PlayerFromContext returns the authenticated player on the request, or
// nil if no session was loaded.
func PlayerFromContext(ctx context.Context) *player.Player {
	p, _ := ctx.Value(playerCtxKey{}).(*player.Player)
	return p
}

// LoadSession reads the player session cookie, validates the refresh
// token, and (if valid) stashes the player on the request context.
// Anonymous requests pass through with no player on the context.
//
// Mirrors designer.LoadSession; kept separate so the two realms can
// evolve independently (e.g. player will likely add gamepad-token
// session types post-v1).
func LoadSession(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(SessionCookieName)
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			p, err := d.Auth.ValidateRefreshSession(r.Context(), c.Value)
			if err != nil {
				if errors.Is(err, player.ErrSessionInvalid) {
					// Cookie is stale; clear it so the browser stops sending it.
					http.SetCookie(w, expiredSessionCookie(d.SecureCookies))
				}
				next.ServeHTTP(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), playerCtxKey{}, p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequirePlayer wraps a handler so it 302-redirects to /play/login when
// no player is on the context. Used by every authenticated route.
func RequirePlayer(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PlayerFromContext(r.Context()) == nil {
			http.Redirect(w, r, "/play/login", http.StatusSeeOther)
			return
		}
		h(w, r)
	})
}

// setSessionCookie writes the player refresh-token cookie. Caller has
// already created the refresh session via player.OpenRefreshSession.
func setSessionCookie(w http.ResponseWriter, secure bool, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    raw,
		Path:     "/",
		MaxAge:   int(player.RefreshTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// expiredSessionCookie builds the Max-Age=-1 clear-cookie value used on
// logout and on stale-token detection.
func expiredSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	}
}
