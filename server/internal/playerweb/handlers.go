package playerweb

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"boxland/server/internal/assets"
	"boxland/server/internal/auth/player"
	"boxland/server/internal/characters"
	"boxland/server/internal/hud"
	"boxland/server/internal/maps"
	"boxland/server/internal/persistence"
	"boxland/server/internal/settings"
	"boxland/server/views"
)

// Deps bundles services the player surface needs. Mirrors the designer
// Deps shape; nil-pointer access at runtime indicates a wiring bug in
// cmd/boxland/main.go.
type Deps struct {
	Auth          *player.Service
	Maps          *maps.Service
	Settings      *settings.Service
	HUD           *hud.Repo // per-realm HUD layouts (read-only on the player surface)
	Assets        *assets.Service             // sprite catalog + animations for the renderer
	ObjectStore   *persistence.ObjectStore    // public URLs for the asset catalog endpoint
	Characters    *characters.Service         // player character creator + catalog
	SecureCookies bool      // true in prod; false in dev so http://localhost works
	WSURL         string    // absolute ws://... or wss://... URL the client opens
	ServerName    string    // displayed under the top nav; "Default server" until multi-tenant
}

// New returns the http.Handler with /play routes mounted. Caller wraps
// the result in CSRF middleware (mirrors designer mount).
func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /play/login", getLogin(d))
	mux.HandleFunc("POST /play/login", postLogin(d))
	mux.HandleFunc("GET /play/signup", getSignup(d))
	mux.HandleFunc("POST /play/signup", postSignup(d))

	auth := func(h http.HandlerFunc) http.Handler { return RequirePlayer(h) }
	mux.Handle("POST /play/logout",       auth(postLogout(d)))
	mux.Handle("GET /play/",              auth(getRoot(d)))
	mux.Handle("GET /play/maps",          auth(getMaps(d)))
	mux.Handle("GET /play/game/{id}",     auth(getGame(d)))
	mux.Handle("GET /play/maps/{id}/hud", auth(getMapHUD(d)))
	mux.Handle("GET /play/asset-catalog",  auth(getAssetCatalog(d)))
	mux.Handle("GET /play/character-catalog", auth(getCharacterCatalog(d)))
	mux.Handle("GET /play/characters", auth(listPlayerCharacters(d)))
	mux.Handle("GET /play/characters/new", auth(getPlayerCharacterGeneratorPage(d)))
	mux.Handle("POST /play/characters", auth(createPlayerCharacter(d)))
	mux.Handle("GET /play/characters/{id}", auth(getPlayerCharacter(d)))
	mux.Handle("GET /play/characters/{id}/edit", auth(getPlayerCharacterEditPage(d)))
	mux.Handle("POST /play/characters/{id}", auth(updatePlayerCharacter(d)))
	mux.Handle("POST /play/ws-ticket",    auth(postWSTicket(d)))
	mux.Handle("GET /play/settings",      auth(getSettingsPage(d)))
	if d.Settings != nil {
		settingsHandlers := &settings.HTTPHandlers{
			Service: d.Settings,
			Resolver: func(r *http.Request) (settings.Realm, int64, bool) {
				p := PlayerFromContext(r.Context())
				if p == nil {
					return "", 0, false
				}
				return settings.RealmPlayer, p.ID, true
			},
		}
		mux.Handle("GET /play/settings/me", auth(settingsHandlers.Get))
		mux.Handle("PUT /play/settings/me", auth(settingsHandlers.Put))
	}

	return mux
}

// ---- Auth pages ----

func getLogin(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, r, views.PlayLoginPage(views.PlayLoginProps{Mode: "login"}))
	}
}

func getSignup(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, r, views.PlayLoginPage(views.PlayLoginProps{Mode: "signup"}))
	}
}

func postLogin(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		email := strings.TrimSpace(r.PostFormValue("email"))
		pw := r.PostFormValue("password")
		p, err := d.Auth.Login(r.Context(), email, pw)
		if err != nil {
			renderLoginError(w, r, "login", email, err)
			return
		}
		if err := openSession(w, r, d, p); err != nil {
			renderLoginError(w, r, "login", email, err)
			return
		}
		http.Redirect(w, r, "/play/maps", http.StatusSeeOther)
	}
}

func postSignup(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		email := strings.TrimSpace(r.PostFormValue("email"))
		pw := r.PostFormValue("password")
		p, err := d.Auth.CreatePlayer(r.Context(), email, pw)
		if err != nil {
			renderLoginError(w, r, "signup", email, err)
			return
		}
		if err := openSession(w, r, d, p); err != nil {
			renderLoginError(w, r, "signup", email, err)
			return
		}
		http.Redirect(w, r, "/play/maps", http.StatusSeeOther)
	}
}

func postLogout(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
			if err := d.Auth.CloseRefreshSession(r.Context(), c.Value); err != nil {
				slog.Warn("playerweb logout: close session", "err", err)
			}
		}
		http.SetCookie(w, expiredSessionCookie(d.SecureCookies))
		http.Redirect(w, r, "/play/login", http.StatusSeeOther)
	}
}

// ---- Map picker + game view ----

func getRoot(_ Deps) http.HandlerFunc {
	// Logged-in landing -> /play/maps. Anonymous never reaches this thanks
	// to RequirePlayer.
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/play/maps", http.StatusSeeOther)
	}
}

func getMaps(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := PlayerFromContext(r.Context())
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		items, err := d.Maps.ListPublic(r.Context(), search)
		if err != nil {
			http.Error(w, "list maps: "+err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, views.PlayMapsPage(views.PlayMapsProps{
			Player:     displayName(p),
			ServerName: serverName(d),
			Items:      items,
			Search:     search,
		}))
	}
}

func getGame(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := PlayerFromContext(r.Context())
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, maps.ErrMapNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "find map: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Only public maps are reachable from the picker; reject direct
		// URL access to private maps so curious players can't discover
		// names by id-bumping.
		if !m.Public {
			http.NotFound(w, r)
			return
		}
		// Mint a fresh access JWT the WS Auth handshake will redeem.
		// Short-lived (15min) by design; the client refreshes it via
		// POST /play/ws-ticket on every reconnect attempt.
		jwt, err := d.Auth.MintAccessToken(p)
		if err != nil {
			http.Error(w, "mint jwt: "+err.Error(), http.StatusInternalServerError)
			return
		}
		render(w, r, views.PlayGamePage(views.PlayGameProps{
			Player:      displayName(p),
			ServerName:  serverName(d),
			Map:         *m,
			WSURL:       resolveWSURL(d.WSURL, r),
			AccessToken: jwt,
		}))
	}
}

// getMapHUD returns the realm's HUD layout as JSON. The play-game page
// fetches this on boot and feeds it to the Pixi HUD layer (see
// web/src/render/hud.ts). Reads the LIVE column on maps; a fresh map
// with no HUD set returns the canonical empty layout rather than a
// hard 404 so the client can mount an empty HUD without special-casing.
//
// Public-realm gating mirrors getGame: only public maps are reachable
// from the player surface. Private maps return 404 so id-bumping can't
// enumerate names.
func getMapHUD(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), id)
		if err != nil || m == nil {
			http.NotFound(w, r)
			return
		}
		if !m.Public {
			http.NotFound(w, r)
			return
		}
		var layout hud.Layout
		if d.HUD != nil {
			l, err := d.HUD.GetForPlayer(r.Context(), id)
			if err != nil {
				http.Error(w, "load hud: "+err.Error(), http.StatusInternalServerError)
				return
			}
			layout = l
		} else {
			layout = hud.NewEmpty()
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store") // HUD changes on publish; never cache
		_ = json.NewEncoder(w).Encode(layout)
	}
}

// getSettingsPage renders the player Settings page. Same Templ as the
// designer surface; the client TS hydrates it via /play/settings/me.
func getSettingsPage(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, r, views.SettingsPage(views.SettingsProps{
			Realm:      "player",
			LoadURL:    "/play/settings/me",
			SaveURL:    "/play/settings/me",
			HideTopNav: true,
		}))
	}
}

// postWSTicket returns a fresh player access JWT as JSON. The web client
// calls this on every (re)connect so the WS Auth handshake never sees
// an expired token. Mirrors the designer ws-ticket endpoint pattern.
func postWSTicket(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := PlayerFromContext(r.Context())
		jwt, err := d.Auth.MintAccessToken(p)
		if err != nil {
			http.Error(w, "mint jwt: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprintf(w, `{"token":%q}`, jwt)
	}
}

// ---- Helpers ----

func openSession(w http.ResponseWriter, r *http.Request, d Deps, p *player.Player) error {
	tok, err := d.Auth.OpenRefreshSession(r.Context(), p.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	setSessionCookie(w, d.SecureCookies, tok)
	return nil
}

func renderLoginError(w http.ResponseWriter, r *http.Request, mode, email string, err error) {
	render(w, r, views.PlayLoginPage(views.PlayLoginProps{
		Mode:      mode,
		Email:     email,
		FormError: humanizeAuthError(err),
	}))
}

func humanizeAuthError(err error) string {
	switch {
	case errors.Is(err, player.ErrEmailInUse):
		return "That email is already registered."
	case errors.Is(err, player.ErrInvalidCredentials):
		return "Invalid email or password."
	default:
		return "Something went wrong. Try again."
	}
}

func displayName(p *player.Player) string {
	if p == nil {
		return ""
	}
	if p.DisplayName != nil && *p.DisplayName != "" {
		return *p.DisplayName
	}
	return p.Email
}

func serverName(d Deps) string {
	if d.ServerName == "" {
		return "Default server"
	}
	return d.ServerName
}

// resolveWSURL returns the absolute WS URL the browser should open.
// If Deps.WSURL is configured (production), use it verbatim. Otherwise
// derive it from the current request: same host, ws/wss scheme, /ws.
func resolveWSURL(configured string, r *http.Request) string {
	if configured != "" {
		return configured
	}
	scheme := "ws"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	return scheme + "://" + r.Host + "/ws"
}

// clientIP mirrors the designer helper. Trusts X-Forwarded-For only when
// the immediate peer is loopback.
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

func render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.Render(r.Context(), w); err != nil {
		slog.Error("playerweb render", "err", err)
	}
}
