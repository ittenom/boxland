// Package designer wires the design-tool HTTP surface: login, signup,
// password reset, the WS-ticket endpoint, and (later) the artifact CRUD
// pages. All routes here run inside the designer realm and require a
// valid session cookie unless otherwise noted.
package designer

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/persistence"
	"boxland/server/internal/publishing/artifact"
	"boxland/server/views"
)

// SessionCookieName is the cookie carrying the designer session token.
const SessionCookieName = "boxland_designer"

// Deps bundles the dependencies designer HTTP handlers need. Fields are
// added incrementally as new surfaces land; nil-pointer access at runtime
// indicates a wiring bug in cmd/boxland/main.go (the only intended Deps
// constructor).
type Deps struct {
	Auth            *authdesigner.Service
	Assets          *assets.Service
	Importers       *assets.Registry
	BakeJob         *assets.BakeJob
	PublishPipeline *artifact.Pipeline
	ObjectStore     *persistence.ObjectStore
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

	// Asset Manager surface (PLAN.md §5c).
	mux.Handle("GET /design/assets",            auth(getAssetsList(d)))
	mux.Handle("GET /design/assets/grid",       auth(getAssetsGrid(d)))
	mux.Handle("GET /design/assets/upload",     auth(getAssetUploadModal(d)))
	mux.Handle("POST /design/assets/upload",    auth(postAssetUpload(d)))
	mux.Handle("GET /design/assets/{id}",       auth(getAssetDetail(d)))
	mux.Handle("DELETE /design/assets/{id}",    auth(deleteAsset(d)))
	mux.Handle("POST /design/assets/{id}/draft",   auth(postAssetDraft(d)))
	mux.Handle("POST /design/assets/{id}/replace", auth(postAssetReplace(d)))

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

// ---- Asset Manager ----

// getAssetsList renders the full Asset Manager page.
func getAssetsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := assetListOptsFromQuery(r)
		items, err := d.Assets.List(r.Context(), opts)
		if err != nil {
			slog.Error("assets list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.AssetsList(views.AssetsListProps{
			Items:      items,
			ActiveKind: string(opts.Kind),
			Search:     opts.Search,
			PublicURL:  d.ObjectStore.PublicURL,
		}))
	}
}

// getAssetsGrid returns just the inner grid HTML for HTMX swaps from the
// search/filter form.
func getAssetsGrid(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := assetListOptsFromQuery(r)
		items, err := d.Assets.List(r.Context(), opts)
		if err != nil {
			slog.Error("assets list (grid)", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.AssetsGrid(views.AssetsListProps{
			Items:      items,
			ActiveKind: string(opts.Kind),
			Search:     opts.Search,
			PublicURL:  d.ObjectStore.PublicURL,
		}))
	}
}

// getAssetUploadModal returns the upload modal HTML for HTMX to swap into
// #modal-host. The data-bx-action="open-upload" button on the list page
// triggers this.
func getAssetUploadModal(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderHTML(w, r, views.AssetUploadModal(views.AssetUploadModalProps{}))
	}
}

// getAssetDetail renders the per-asset modal.
func getAssetDetail(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := assetIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		a, err := d.Assets.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		}
		renderHTML(w, r, views.AssetDetail(views.AssetDetailProps{
			Asset:     *a,
			PublicURL: d.ObjectStore.PublicURL,
		}))
	}
}

// deleteAsset deletes the asset row and returns the refreshed grid HTML
// so the calling HTMX request can swap it in place.
func deleteAsset(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := assetIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Assets.Delete(r.Context(), id); err != nil {
			if errors.Is(err, assets.ErrAssetNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("asset delete", "err", err, "id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Re-render grid with whatever filters are in play (none on a DELETE,
		// so just an empty filter set).
		items, err := d.Assets.List(r.Context(), assets.ListOpts{})
		if err != nil {
			slog.Error("assets list (after delete)", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.AssetsGrid(views.AssetsListProps{
			Items:     items,
			PublicURL: d.ObjectStore.PublicURL,
		}))
	}
}

// postAssetDraft persists an AssetDraft into the drafts table. The publish
// pipeline applies it later via "Push to Live". For now we return a small
// toast confirming the save.
func postAssetDraft(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := assetIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Form fields from the generic form renderer:
		//   name             single value
		//   tags[0].tag      ... list field, stub renderer (one row in v1)
		//
		// v1: we accept a comma-separated tag string in `tags` for ergonomics
		// while the list editor catches up; the descriptor still drives the
		// renderer.
		draft := assets.AssetDraft{
			Name: strings.TrimSpace(r.FormValue("name")),
			Tags: parseTags(r.FormValue("tags")),
		}
		// If list-renderer style fields are present, prefer them.
		if first := strings.TrimSpace(r.FormValue("tags[0].tag")); first != "" {
			draft.Tags = []string{first}
		}
		if err := draft.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := json.Marshal(draft)
		if err != nil {
			http.Error(w, "marshal draft", http.StatusInternalServerError)
			return
		}
		if _, err := d.Assets.Pool.Exec(r.Context(), `
			INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (artifact_kind, artifact_id) DO UPDATE
			SET draft_json = EXCLUDED.draft_json,
			    updated_at = now()
		`, "asset", id, body, dr.ID); err != nil {
			slog.Error("save draft", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(
			`<div class="bx-toast bx-toast--success" data-copy-slot="assets.draft.saved">Draft saved.</div>`,
		))
	}
}

// assetListOptsFromQuery turns querystring parameters into ListOpts.
func assetListOptsFromQuery(r *http.Request) assets.ListOpts {
	q := r.URL.Query()
	opts := assets.ListOpts{
		Kind:   assets.Kind(q.Get("kind")),
		Search: strings.TrimSpace(q.Get("q")),
		Limit:  100, // page size; Cmd-K-style infinite scroll lands later
	}
	if tags := q.Get("tags"); tags != "" {
		opts.Tags = parseTags(tags)
	}
	return opts
}

func assetIDFromPath(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	if idStr == "" {
		return 0, errors.New("missing asset id")
	}
	var id int64
	if _, err := fmt.Sscan(idStr, &id); err != nil {
		return 0, fmt.Errorf("invalid asset id %q", idStr)
	}
	return id, nil
}

// parseTags turns a comma- or whitespace-separated tag list into a slice.
// Empty strings are filtered out so blank input becomes nil, not [""].
func parseTags(raw string) []string {
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// postAssetReplace accepts an exported PNG buffer from the pixel editor.
// Behavior:
//   * Treats the upload as a fresh asset of the same kind (new
//     content-addressed path; the original asset row is unchanged).
//   * Triggers re-bake of the new asset's palette variants (it has none
//     unless the designer copied them later — handled by a separate
//     "duplicate variants" surface).
//
// Why a NEW asset row rather than mutating the existing one?
//   * Content-addressed paths are immutable — the old row's path keeps its
//     bytes intact for any in-flight references.
//   * Designers can compare old vs new before swapping references.
//   * Avoids a destructive flow that's hard to undo.
//
// The original asset id is returned alongside the new one so the UI can
// surface a "Replace references with the new asset?" follow-up.
func postAssetReplace(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		oldID, err := assetIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		old, err := d.Assets.FindByID(r.Context(), oldID)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Reuse the upload pipeline so dedup, normalization, and the bake
		// trigger paths stay in one place. Force the kind to match the old
		// asset so a sprite stays a sprite even if the new bytes would
		// auto-detect to "tile".
		res, err := d.Assets.Upload(r.Context(), r, d.ObjectStore, dr.ID, old.Kind)
		if err != nil {
			status := http.StatusBadRequest
			switch {
			case errors.Is(err, assets.ErrTooLarge):
				status = http.StatusRequestEntityTooLarge
			case errors.Is(err, assets.ErrUnsupportedContentType):
				status = http.StatusUnsupportedMediaType
			}
			slog.Warn("asset replace", "err", err)
			http.Error(w, err.Error(), status)
			return
		}

		// Bake any palette variants the *new* asset already has. Newly-uploaded
		// rows start with no variants; this is a no-op unless the upload
		// dedup'd back to an existing asset that already has variants.
		if _, err := d.BakeJob.BakeForAsset(r.Context(), res.Asset.ID); err != nil {
			slog.Warn("asset replace: bake", "err", err, "asset_id", res.Asset.ID)
			// Non-fatal: the asset itself was created. Surface the warning
			// to the client so they know the next push-to-live may republish
			// stale variants.
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"old_asset_id": oldID,
			"new_asset":    res.Asset,
			"reused":       res.Reused,
		})
	}
}

// ---- Asset upload ----

// postAssetUpload accepts a multipart form upload, pushes the file into
// object storage at a content-addressed path, creates the asset row, and
// (for sprite/tile kinds) returns the auto-detected importer id so the
// client can offer a one-click "import frames" follow-up.
//
// Response is JSON for HTMX-easy consumption. Stable shape:
//
//	{
//	  "asset": { "id": ..., "kind": "...", "name": "...",
//	             "content_addressed_path": "...", ... },
//	  "reused": bool,
//	  "warning": { "problem": "...", "severity": "warn|error", "message": "..." } | null,
//	  "suggested_importer": "raw"|"strip"|"aseprite"|... | ""
//	}
func postAssetUpload(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Optional kind override from a query string (?kind=tile etc).
		kindOverride := assets.Kind(r.URL.Query().Get("kind"))

		res, err := d.Assets.Upload(r.Context(), r, d.ObjectStore, dr.ID, kindOverride)
		if err != nil {
			status := http.StatusBadRequest
			switch {
			case errors.Is(err, assets.ErrTooLarge):
				status = http.StatusRequestEntityTooLarge
			case errors.Is(err, assets.ErrUnsupportedContentType):
				status = http.StatusUnsupportedMediaType
			case errors.Is(err, assets.ErrNoFile):
				status = http.StatusBadRequest
			}
			slog.Warn("asset upload", "err", err, "designer_id", dr.ID)
			http.Error(w, err.Error(), status)
			return
		}

		// HTMX request → render the styled toast partial; non-HTMX (e.g.
		// curl, future API consumers) → JSON.
		if r.Header.Get("HX-Request") == "true" {
			renderHTML(w, r, views.AssetUploadResult(views.AssetUploadResultProps{
				AssetID: res.Asset.ID,
				Name:    res.Asset.Name,
				Kind:    string(res.Asset.Kind),
				Reused:  res.Reused,
			}))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"asset":   res.Asset,
			"reused":  res.Reused,
			"warning": nil,
		})
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
