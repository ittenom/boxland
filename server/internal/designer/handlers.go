// Package designer wires the design-tool HTTP surface: login, signup,
// password reset, the WS-ticket endpoint, and (later) the artifact CRUD
// pages. All routes here run inside the designer realm and require a
// valid session cookie unless otherwise noted.
package designer

import (
	"context"
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
	"boxland/server/internal/configurable"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
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
	Entities        *entities.Service
	Components      *components.Registry
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

	// Entity Manager surface (PLAN.md §5d).
	mux.Handle("GET /design/entities",                       auth(getEntitiesList(d)))
	mux.Handle("GET /design/entities/grid",                  auth(getEntitiesGrid(d)))
	mux.Handle("GET /design/entities/new",                   auth(getEntityNewModal(d)))
	mux.Handle("POST /design/entities",                      auth(postEntityCreate(d)))
	mux.Handle("GET /design/entities/{id}",                  auth(getEntityDetail(d)))
	mux.Handle("DELETE /design/entities/{id}",               auth(deleteEntity(d)))
	mux.Handle("POST /design/entities/{id}/draft",           auth(postEntityDraft(d)))
	mux.Handle("POST /design/entities/{id}/duplicate",       auth(postEntityDuplicate(d)))
	mux.Handle("POST /design/entities/{id}/components/add",  auth(postEntityComponentAdd(d)))
	mux.Handle("POST /design/entities/{id}/components/{kind}", auth(postEntityComponentSave(d)))
	mux.Handle("DELETE /design/entities/{id}/components/{kind}", auth(deleteEntityComponent(d)))

	// Edge sockets surface (PLAN.md §4e + §5d).
	mux.Handle("GET /design/sockets",         auth(getSocketsList(d)))
	mux.Handle("POST /design/sockets",        auth(postSocketCreate(d)))
	mux.Handle("DELETE /design/sockets/{id}", auth(deleteSocket(d)))

	// Tile groups surface (PLAN.md §4e + §5d).
	mux.Handle("GET /design/tile-groups",                auth(getTileGroupsList(d)))
	mux.Handle("POST /design/tile-groups",               auth(postTileGroupCreate(d)))
	mux.Handle("GET /design/tile-groups/{id}",           auth(getTileGroupDetail(d)))
	mux.Handle("DELETE /design/tile-groups/{id}",        auth(deleteTileGroup(d)))
	mux.Handle("POST /design/tile-groups/{id}/layout",   auth(postTileGroupLayout(d)))

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

// ---- Entity Manager ----

func getEntitiesList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := entityListOptsFromQuery(r)
		items, err := d.Entities.List(r.Context(), opts)
		if err != nil {
			slog.Error("entities list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.EntitiesList(views.EntitiesListProps{
			Items:  items,
			Search: opts.Search,
		}))
	}
}

func getEntitiesGrid(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := entityListOptsFromQuery(r)
		items, err := d.Entities.List(r.Context(), opts)
		if err != nil {
			slog.Error("entities grid", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{
			Items:  items,
			Search: opts.Search,
		}))
	}
}

// getEntityNewModal renders a tiny "create new entity type" form. The
// detail editor (with components panel) lands as soon as the row exists.
func getEntityNewModal(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Reuse the upload-modal CSS conventions; bespoke template would be
		// overkill for a single-input prompt.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<div class="bx-modal-backdrop" data-bx-dismissible role="dialog" aria-modal="true">
  <div class="bx-modal">
    <header class="bx-modal__header">
      <h2 data-copy-slot="entities.new.title">New entity type</h2>
      <button type="button" class="bx-btn bx-btn--ghost"
              hx-on:click="this.closest('.bx-modal-backdrop').remove()"
              aria-label="Close">Esc</button>
    </header>
    <div class="bx-modal__body">
      <form hx-post="/design/entities" hx-target="#entities-grid" hx-swap="outerHTML"
            hx-on:htmx:after-request="this.closest('.bx-modal-backdrop')?.remove()"
            class="bx-stack">
        <div class="bx-field">
          <label for="new-name" class="bx-label" data-copy-slot="entities.new.name">Name</label>
          <input id="new-name" name="name" class="bx-input" required maxlength="128" autofocus>
        </div>
        <div class="bx-row bx-row--end">
          <button type="submit" class="bx-btn bx-btn--primary" data-copy-slot="entities.new.submit">Create</button>
        </div>
      </form>
    </div>
  </div>
</div>
`))
	}
}

func postEntityCreate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		if _, err := d.Entities.Create(r.Context(), entities.CreateInput{
			Name:      name,
			CreatedBy: dr.ID,
		}); err != nil {
			if errors.Is(err, entities.ErrNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("entity create", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Re-render grid with default filter.
		items, _ := d.Entities.List(r.Context(), entities.ListOpts{})
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{Items: items}))
	}
}

func getEntityDetail(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		et, err := d.Entities.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		comps, err := d.Entities.Components(r.Context(), id)
		if err != nil {
			slog.Error("entity components", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		props := views.EntityDetailProps{
			EntityType:  *et,
			Components:  comps,
			AllKinds:    d.Components.Kinds(),
			Descriptors: collectDescriptors(d.Components),
			SpriteURL:   spriteURLFor(d, et),
		}
		renderHTML(w, r, views.EntityDetail(props))
	}
}

func deleteEntity(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Entities.Delete(r.Context(), id); err != nil {
			if errors.Is(err, entities.ErrEntityTypeNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("entity delete", "err", err, "id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items, _ := d.Entities.List(r.Context(), entities.ListOpts{})
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{Items: items}))
	}
}

func postEntityDraft(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Form-derived draft. Components edits go through their own per-kind
		// endpoint (postEntityComponentSave); this draft only carries the
		// top-level fields.
		draft := entities.EntityTypeDraft{
			Name:                 strings.TrimSpace(r.FormValue("name")),
			ColliderW:            int32(parseIntOr(r.FormValue("collider_w"), 0)),
			ColliderH:            int32(parseIntOr(r.FormValue("collider_h"), 0)),
			ColliderAnchorX:      int32(parseIntOr(r.FormValue("collider_anchor_x"), 0)),
			ColliderAnchorY:      int32(parseIntOr(r.FormValue("collider_anchor_y"), 0)),
			DefaultCollisionMask: int64(parseIntOr(r.FormValue("default_collision_mask"), 1)),
			Tags:                 parseTags(r.FormValue("tags")),
		}
		if v := strings.TrimSpace(r.FormValue("sprite_asset_id")); v != "" {
			if id, err := strconvAtoi64(v); err == nil {
				draft.SpriteAssetID = &id
			}
		}
		if v := strings.TrimSpace(r.FormValue("default_animation_id")); v != "" {
			if id, err := strconvAtoi64(v); err == nil {
				draft.DefaultAnimationID = &id
			}
		}
		if first := strings.TrimSpace(r.FormValue("tags[0].tag")); first != "" {
			draft.Tags = []string{first}
		}
		if err := draft.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := json.Marshal(draft)
		if _, err := d.Entities.Pool.Exec(r.Context(), `
			INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (artifact_kind, artifact_id) DO UPDATE
			SET draft_json = EXCLUDED.draft_json, updated_at = now()
		`, "entity_type", id, body, dr.ID); err != nil {
			slog.Error("entity draft save", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="bx-toast bx-toast--success">Draft saved.</div>`))
	}
}

func postEntityDuplicate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		original, err := d.Entities.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Loop until we land an unused name. Simple suffix walker.
		newName := original.Name + " (copy)"
		for i := 2; i < 100; i++ {
			if _, err := d.Entities.FindByName(r.Context(), newName); errors.Is(err, entities.ErrEntityTypeNotFound) {
				break
			}
			newName = fmt.Sprintf("%s (copy %d)", original.Name, i)
		}

		copy, err := d.Entities.Create(r.Context(), entities.CreateInput{
			Name:                 newName,
			SpriteAssetID:        original.SpriteAssetID,
			DefaultAnimationID:   original.DefaultAnimationID,
			ColliderW:            original.ColliderW,
			ColliderH:            original.ColliderH,
			ColliderAnchorX:      original.ColliderAnchorX,
			ColliderAnchorY:      original.ColliderAnchorY,
			DefaultCollisionMask: original.DefaultCollisionMask,
			Tags:                 original.Tags,
			CreatedBy:            dr.ID,
		})
		if err != nil {
			slog.Error("entity duplicate", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Deep-clone components.
		comps, _ := d.Entities.Components(r.Context(), original.ID)
		if len(comps) > 0 {
			cfg := make(map[components.Kind]json.RawMessage, len(comps))
			for _, c := range comps {
				cfg[c.Kind] = c.ConfigJSON
			}
			if err := d.Entities.SetComponents(r.Context(), nil, copy.ID, cfg); err != nil {
				slog.Warn("entity duplicate: component clone", "err", err)
			}
		}

		items, _ := d.Entities.List(r.Context(), entities.ListOpts{})
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{Items: items}))
	}
}

// postEntityComponentAdd attaches a fresh empty config for the picked kind
// and re-renders the detail modal so the new component editor appears.
func postEntityComponentAdd(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		kindStr := strings.TrimSpace(r.FormValue("kind"))
		if kindStr == "" {
			http.Error(w, "kind is required", http.StatusBadRequest)
			return
		}
		kind := components.Kind(kindStr)
		def, ok := d.Components.Get(kind)
		if !ok {
			http.Error(w, "unknown component kind", http.StatusBadRequest)
			return
		}
		raw, err := json.Marshal(def.Default())
		if err != nil {
			http.Error(w, "marshal default", http.StatusInternalServerError)
			return
		}
		// Append, do NOT replace the rest. We do this via raw SQL because
		// SetComponents replaces; here we want to insert one row.
		if _, err := d.Entities.Pool.Exec(r.Context(), `
			INSERT INTO entity_components (entity_type_id, component_kind, config_json)
			VALUES ($1, $2, $3::jsonb)
			ON CONFLICT (entity_type_id, component_kind) DO NOTHING
		`, id, string(kind), raw); err != nil {
			slog.Error("component add", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Re-render the whole detail modal so the new editor appears in place.
		et, err := d.Entities.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		comps, _ := d.Entities.Components(r.Context(), id)
		renderHTML(w, r, views.EntityDetail(views.EntityDetailProps{
			EntityType:  *et,
			Components:  comps,
			AllKinds:    d.Components.Kinds(),
			Descriptors: collectDescriptors(d.Components),
			SpriteURL:   spriteURLFor(d, et),
		}))
	}
}

func postEntityComponentSave(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		kind := components.Kind(r.PathValue("kind"))
		def, ok := d.Components.Get(kind)
		if !ok {
			http.Error(w, "unknown component kind", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Build a JSON payload from form values, typed by the descriptor.
		raw, err := jsonFromFormByDescriptor(def.Descriptor(), r.Form)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := def.Validate(raw); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := d.Entities.Pool.Exec(r.Context(), `
			INSERT INTO entity_components (entity_type_id, component_kind, config_json)
			VALUES ($1, $2, $3::jsonb)
			ON CONFLICT (entity_type_id, component_kind) DO UPDATE
			SET config_json = EXCLUDED.config_json
		`, id, string(kind), raw); err != nil {
			slog.Error("component save", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="bx-toast bx-toast--success">Component saved.</div>`))
	}
}

func deleteEntityComponent(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		kind := r.PathValue("kind")
		if _, err := d.Entities.Pool.Exec(r.Context(),
			`DELETE FROM entity_components WHERE entity_type_id = $1 AND component_kind = $2`,
			id, kind,
		); err != nil {
			slog.Error("component delete", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Empty response so the editor row disappears via outerHTML swap.
		w.WriteHeader(http.StatusOK)
	}
}

// ---- entity helpers ----

func entityListOptsFromQuery(r *http.Request) entities.ListOpts {
	q := r.URL.Query()
	opts := entities.ListOpts{
		Search: strings.TrimSpace(q.Get("q")),
		Limit:  100,
	}
	if tags := q.Get("tags"); tags != "" {
		opts.Tags = parseTags(tags)
	}
	return opts
}

func pathID(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	if idStr == "" {
		return 0, errors.New("missing id")
	}
	id, err := strconvAtoi64(idStr)
	if err != nil {
		return 0, fmt.Errorf("invalid id %q", idStr)
	}
	return id, nil
}

func strconvAtoi64(s string) (int64, error) {
	var n int64
	if _, err := fmt.Sscan(s, &n); err != nil {
		return 0, err
	}
	return n, nil
}

func parseIntOr(s string, def int64) int64 {
	if s == "" {
		return def
	}
	n, err := strconvAtoi64(s)
	if err != nil {
		return def
	}
	return n
}

func collectDescriptors(reg *components.Registry) map[components.Kind][]configurableFieldDescriptor {
	out := make(map[components.Kind][]configurableFieldDescriptor, len(reg.Kinds()))
	for _, k := range reg.Kinds() {
		def, _ := reg.Get(k)
		out[k] = def.Descriptor()
	}
	return out
}

// configurableFieldDescriptor is an alias to keep the public configurable
// type the canonical one but make local declarations less verbose.
type configurableFieldDescriptor = configurable.FieldDescriptor

func spriteURLFor(d Deps, et *entities.EntityType) string {
	if et.SpriteAssetID == nil || d.Assets == nil {
		return ""
	}
	a, err := d.Assets.FindByID(context.Background(), *et.SpriteAssetID)
	if err != nil {
		return ""
	}
	return d.ObjectStore.PublicURL(a.ContentAddressedPath)
}

// ---- Edge sockets ----

func getSocketsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := d.Entities.ListSockets(r.Context())
		if err != nil {
			slog.Error("sockets list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.SocketsList(views.SocketsListProps{Items: items}))
	}
}

func postSocketCreate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		color := parseHexColor(r.FormValue("color"))
		_, err := d.Entities.CreateSocket(r.Context(), r.FormValue("name"), color, dr.ID)
		if err != nil {
			if errors.Is(err, entities.ErrSocketNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("socket create", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items, _ := d.Entities.ListSockets(r.Context())
		renderHTML(w, r, views.SocketsGrid(views.SocketsListProps{Items: items}))
	}
}

func deleteSocket(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Entities.DeleteSocket(r.Context(), id); err != nil {
			if errors.Is(err, entities.ErrSocketNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("socket delete", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items, _ := d.Entities.ListSockets(r.Context())
		renderHTML(w, r, views.SocketsGrid(views.SocketsListProps{Items: items}))
	}
}

// parseHexColor turns "#rrggbb" (from <input type="color">) into a
// 0xRRGGBBAA int64. Alpha defaults to 0xff.
func parseHexColor(s string) int64 {
	s = strings.TrimSpace(s)
	if len(s) >= 1 && s[0] == '#' {
		s = s[1:]
	}
	if len(s) != 6 {
		return 0xffd34aff
	}
	var v int64
	if _, err := fmt.Sscanf(s, "%x", &v); err != nil {
		return 0xffd34aff
	}
	return (v << 8) | 0xff
}

// ---- Tile groups ----

func getTileGroupsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := d.Entities.ListTileGroups(r.Context())
		if err != nil {
			slog.Error("tile groups list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.TileGroupsList(views.TileGroupsListProps{Items: items}))
	}
}

func postTileGroupCreate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, err := d.Entities.CreateTileGroup(r.Context(), entities.CreateTileGroupInput{
			Name:      r.FormValue("name"),
			Width:     int32(parseIntOr(r.FormValue("width"), 3)),
			Height:    int32(parseIntOr(r.FormValue("height"), 2)),
			CreatedBy: dr.ID,
		})
		if err != nil {
			if errors.Is(err, entities.ErrTileGroupNameUsed) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("tile group create", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, _ := d.Entities.ListTileGroups(r.Context())
		renderHTML(w, r, views.TileGroupsGrid(views.TileGroupsListProps{Items: items}))
	}
}

func getTileGroupDetail(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tg, err := d.Entities.FindTileGroupByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var layout entities.Layout
		if err := json.Unmarshal(tg.LayoutJSON, &layout); err != nil {
			// Stored layout malformed; fall back to empty so the UI still renders.
			layout = make(entities.Layout, tg.Height)
			for r := range layout {
				layout[r] = make([]int64, tg.Width)
			}
		}
		renderHTML(w, r, views.TileGroupDetail(views.TileGroupDetailProps{
			Group: *tg, Layout: layout,
		}))
	}
}

func deleteTileGroup(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Entities.DeleteTileGroup(r.Context(), id); err != nil {
			if errors.Is(err, entities.ErrTileGroupNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("tile group delete", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items, _ := d.Entities.ListTileGroups(r.Context())
		renderHTML(w, r, views.TileGroupsGrid(views.TileGroupsListProps{Items: items}))
	}
}

func postTileGroupLayout(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var layout entities.Layout
		if err := json.Unmarshal([]byte(r.FormValue("layout")), &layout); err != nil {
			http.Error(w, "bad layout json", http.StatusBadRequest)
			return
		}
		if err := d.Entities.UpdateTileGroupLayout(r.Context(), id, layout); err != nil {
			if errors.Is(err, entities.ErrLayoutSize) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			slog.Error("tile group layout", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<div class="bx-toast bx-toast--success">Layout saved.</div>`))
	}
}

// jsonFromFormByDescriptor turns a flat form (string values) into a typed
// JSON object using the FieldDescriptor as the type oracle. Recurses into
// nested children using "parent.child" form-name conventions.
func jsonFromFormByDescriptor(fields []configurable.FieldDescriptor, form map[string][]string) (json.RawMessage, error) {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		v, ok := form[f.Key]
		if !ok || len(v) == 0 {
			continue
		}
		raw := strings.TrimSpace(v[0])
		switch f.Kind {
		case configurable.KindString, configurable.KindMultilineText, configurable.KindEnum, configurable.KindColor, configurable.KindAssetRef, configurable.KindEntityTypeRef:
			out[f.Key] = raw
		case configurable.KindBool:
			out[f.Key] = raw == "true" || raw == "on" || raw == "1"
		case configurable.KindInt:
			n, _ := strconvAtoi64(raw)
			out[f.Key] = n
		case configurable.KindFloat:
			var f64 float64
			_, _ = fmt.Sscanf(raw, "%f", &f64)
			out[f.Key] = f64
		default:
			// Vec2, Range, Nested, List: not used by the v1 component
			// configs; fall through to leaving the field unset.
		}
	}
	return json.Marshal(out)
}

// ---- Asset replace (continues below) ----

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
