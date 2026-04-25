// Package designer wires the design-tool HTTP surface: login, signup,
// password reset, the WS-ticket endpoint, and (later) the artifact CRUD
// pages. All routes here run inside the designer realm and require a
// valid session cookie unless otherwise noted.
package designer

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/views"
)

// SessionCookieName is the cookie carrying the designer session token.
const SessionCookieName = "boxland_designer"

// Deps bundles the dependencies designer HTTP handlers need.
type Deps struct {
	Auth *authdesigner.Service
}

// New returns an http.Handler with the designer routes mounted under
// /design. The caller wraps the result in CSRF + LoadSession middleware
// (see httpserver wiring in cmd/boxland/main.go).
func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	// Public (no auth required)
	mux.HandleFunc("GET /design/login", getLogin(d))
	mux.HandleFunc("POST /design/login", postLogin(d))
	mux.HandleFunc("GET /design/signup", getSignup(d))
	mux.HandleFunc("POST /design/signup", postSignup(d))

	// Authenticated
	auth := func(h http.HandlerFunc) http.Handler { return RequireDesigner(h) }
	mux.Handle("GET /design/", auth(getShellHome(d)))
	mux.Handle("POST /design/logout", auth(postLogout(d)))
	mux.Handle("POST /design/ws-ticket", auth(postWSTicket(d)))

	return mux
}

// postWSTicket mints a one-shot WS ticket bound to the calling designer +
// IP. Requires a valid session cookie (enforced by the RequireDesigner
// wrapper); reads the designer from the request context.
func postWSTicket(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ip := clientIP(r)
		if ip == nil {
			http.Error(w, "ws-ticket: no client ip", http.StatusBadRequest)
			return
		}
		tok, err := d.Auth.MintWSTicket(r.Context(), dr.ID, ip)
		if err != nil {
			slog.Error("ws-ticket: mint", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"ticket": tok})
	}
}

// ---- Login ----

func getLogin(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if CurrentDesigner(r.Context()) != nil {
			http.Redirect(w, r, "/design/", http.StatusSeeOther)
			return
		}
		renderHTML(w, r, views.LoginPage(views.LoginProps{Mode: "login"}))
	}
}

func postLogin(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := strings.TrimSpace(r.PostFormValue("email"))
		password := r.PostFormValue("password")
		dr, err := d.Auth.Login(r.Context(), email, password)
		if err != nil {
			if errors.Is(err, authdesigner.ErrInvalidCredentials) {
				w.WriteHeader(http.StatusUnauthorized)
				renderHTML(w, r, views.LoginPage(views.LoginProps{
					Mode:      "login",
					Email:     email,
					FormError: "Email or password is incorrect.",
				}))
				return
			}
			slog.Error("login", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := openSessionCookie(w, r, d, dr.ID); err != nil {
			slog.Error("open session", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/design/", http.StatusSeeOther)
	}
}

// ---- Signup ----

func getSignup(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if CurrentDesigner(r.Context()) != nil {
			http.Redirect(w, r, "/design/", http.StatusSeeOther)
			return
		}
		renderHTML(w, r, views.LoginPage(views.LoginProps{Mode: "signup"}))
	}
}

func postSignup(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := strings.TrimSpace(r.PostFormValue("email"))
		password := r.PostFormValue("password")
		if len(password) < 8 {
			w.WriteHeader(http.StatusBadRequest)
			renderHTML(w, r, views.LoginPage(views.LoginProps{
				Mode:      "signup",
				Email:     email,
				FormError: "Password must be at least 8 characters.",
			}))
			return
		}
		// First designer becomes owner; later signups are editors.
		// (Owner-promotion UI lands when the role-management surface ships.)
		role := authdesigner.RoleEditor
		exists, err := d.Auth.HasAnyDesigner(r.Context())
		if err != nil {
			slog.Error("signup: count designers", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !exists {
			role = authdesigner.RoleOwner
		}
		dr, err := d.Auth.CreateDesigner(r.Context(), email, password, role)
		if err != nil {
			if errors.Is(err, authdesigner.ErrEmailInUse) {
				w.WriteHeader(http.StatusConflict)
				renderHTML(w, r, views.LoginPage(views.LoginProps{
					Mode:      "signup",
					Email:     email,
					FormError: "That email is already registered.",
				}))
				return
			}
			slog.Error("signup", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := openSessionCookie(w, r, d, dr.ID); err != nil {
			slog.Error("open session", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/design/", http.StatusSeeOther)
	}
}

// ---- Logout ----

func postLogout(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(SessionCookieName); err == nil {
			_ = d.Auth.CloseSession(r.Context(), cookie.Value)
		}
		http.SetCookie(w, expiredSessionCookie())
		http.Redirect(w, r, "/design/login", http.StatusSeeOther)
	}
}

// ---- Shell home ----

// getShellHome serves the post-login landing page. Other /design/* surfaces
// (assets, entities, ...) get their own routes as they land; this is the
// catch-all for /design/ and /design/{anything-not-mapped}.
func getShellHome(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		renderHTML(w, r, views.ShellHome(views.ShellProps{Designer: dr}))
	}
}

// ---- helpers ----

// openSessionCookie mints a session for the given designer and writes the
// cookie. Cookie attributes mirror PLAN.md §4b: HttpOnly, SameSite=Strict,
// Secure in prod.
func openSessionCookie(w http.ResponseWriter, r *http.Request, d Deps, designerID int64) error {
	tok, err := d.Auth.OpenSession(r.Context(), designerID, r.UserAgent(), clientIP(r))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(authdesigner.SessionTTL.Seconds()),
	})
	return nil
}

// renderHTML is a thin templ-component renderer that writes to the response
// writer with appropriate headers.
func renderHTML(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Error("render", "err", err, "path", r.URL.Path)
	}
}

// clientIP returns the most likely client IP for the request. Honors
// X-Forwarded-For only when the immediate peer is loopback (i.e., a trusted
// dev proxy). In production this should be tightened by the deployment's
// proxy config; the heuristic here is "trust loopback peer to set XFF".
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)

	if peer != nil && peer.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip
			}
		}
	}
	return peer
}
