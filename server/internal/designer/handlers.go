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
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"boxland/server/internal/assets"
	authdesigner "boxland/server/internal/auth/designer"
	"boxland/server/internal/automations"
	"boxland/server/internal/configurable"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/flags"
	"boxland/server/internal/hud"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/maps/wfc"
	"boxland/server/internal/persistence"
	"boxland/server/internal/publishing/artifact"
	"boxland/server/internal/settings"
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
	Maps            *mapsservice.Service
	Importers       *assets.Registry
	BakeJob         *assets.BakeJob
	PublishPipeline *artifact.Pipeline
	ObjectStore     *persistence.ObjectStore
	Settings        *settings.Service

	// Automation editor wiring (PLAN.md §4i + §5d). The two registries
	// drive the form renderer for triggers/actions; the service
	// persists the AutomationSet under entity_automations.
	Automations        *automations.Service
	AutomationTriggers *automations.Registry
	AutomationActions  *automations.Registry

	// Per-realm automation extras: shared "common events" (callable
	// trigger groups) and per-realm flags (switches + variables).
	// See server/internal/automations/groups_repo.go and
	// server/internal/flags. Used by the HUD editor to populate
	// binding + action_group pickers.
	ActionGroups *automations.GroupsRepo
	Flags        *flags.Service

	// Per-realm HUD layouts. The mapmaker HUD editor reads + mutates
	// this; the publish-time validator cross-checks bindings against
	// Flags + ActionGroups before allowing a save.
	HUD        *hud.Repo
	HUDWidgets *hud.Registry
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
	mux.Handle("GET /design/assets", auth(getAssetsList(d)))
	mux.Handle("GET /design/assets/grid", auth(getAssetsGrid(d)))
	mux.Handle("GET /design/assets/blob/{id}", auth(getAssetBlob(d)))
	mux.Handle("GET /design/assets/upload", auth(getAssetUploadModal(d)))
	mux.Handle("POST /design/assets/upload", auth(postAssetUpload(d)))
	mux.Handle("GET /design/assets/{id}", auth(getAssetDetail(d)))
	mux.Handle("DELETE /design/assets/{id}", auth(deleteAsset(d)))
	mux.Handle("POST /design/assets/{id}/draft", auth(postAssetDraft(d)))
	mux.Handle("POST /design/assets/{id}/replace", auth(postAssetReplace(d)))
	mux.Handle("POST /design/assets/{id}/promote-to-entity", auth(postAssetPromoteToEntity(d)))
	mux.Handle("POST /design/assets/promote-bulk", auth(postAssetPromoteBulk(d)))
	mux.Handle("POST /design/assets/delete-bulk", auth(postAssetDeleteBulk(d)))

	// Entity Manager surface (PLAN.md §5d).
	mux.Handle("GET /design/entities", auth(getEntitiesList(d)))
	mux.Handle("GET /design/entities/grid", auth(getEntitiesGrid(d)))
	mux.Handle("GET /design/entities/new", auth(getEntityNewModal(d)))
	mux.Handle("POST /design/entities", auth(postEntityCreate(d)))
	mux.Handle("GET /design/entities/{id}", auth(getEntityDetail(d)))
	mux.Handle("DELETE /design/entities/{id}", auth(deleteEntity(d)))
	mux.Handle("POST /design/entities/{id}/draft", auth(postEntityDraft(d)))
	mux.Handle("POST /design/entities/{id}/duplicate", auth(postEntityDuplicate(d)))
	mux.Handle("POST /design/entities/delete-bulk", auth(postEntityDeleteBulk(d)))
	mux.Handle("POST /design/entities/tag-bulk", auth(postEntityTagBulk(d)))
	mux.Handle("POST /design/entities/{id}/components/add", auth(postEntityComponentAdd(d)))
	mux.Handle("POST /design/entities/{id}/components/{kind}", auth(postEntityComponentSave(d)))
	mux.Handle("DELETE /design/entities/{id}/components/{kind}", auth(deleteEntityComponent(d)))

	// Automation editor (PLAN.md §4i, §5d). Per-entity-type AutomationSet:
	// add/save/delete one automation; the surface is rendered inline on
	// the entity-detail page.
	mux.Handle("POST /design/entities/{id}/automations/add", auth(postAutomationAdd(d)))
	mux.Handle("POST /design/entities/{id}/automations/{idx}", auth(postAutomationSave(d)))
	mux.Handle("DELETE /design/entities/{id}/automations/{idx}", auth(deleteAutomation(d)))
	mux.Handle("POST /design/entities/{id}/automations/{idx}/actions/add", auth(postAutomationActionAdd(d)))
	mux.Handle("DELETE /design/entities/{id}/automations/{idx}/actions/{aIdx}", auth(deleteAutomationAction(d)))

	// Edge sockets surface (PLAN.md §4e + §5d).
	mux.Handle("GET /design/sockets", auth(getSocketsList(d)))
	mux.Handle("POST /design/sockets", auth(postSocketCreate(d)))
	mux.Handle("DELETE /design/sockets/{id}", auth(deleteSocket(d)))

	// Tile groups surface (PLAN.md §4e + §5d).
	mux.Handle("GET /design/tile-groups", auth(getTileGroupsList(d)))
	mux.Handle("POST /design/tile-groups", auth(postTileGroupCreate(d)))
	mux.Handle("GET /design/tile-groups/{id}", auth(getTileGroupDetail(d)))
	mux.Handle("DELETE /design/tile-groups/{id}", auth(deleteTileGroup(d)))
	mux.Handle("POST /design/tile-groups/{id}/layout", auth(postTileGroupLayout(d)))

	// Mapmaker (PLAN.md §5e). Painting is over WS (DesignerCommand
	// PlaceTiles/EraseTiles/PlaceLighting); these HTTP routes only
	// handle list + create + detail + delete.
	mux.Handle("GET /design/maps", auth(getMapsList(d)))
	mux.Handle("GET /design/maps/grid", auth(getMapsGrid(d)))
	mux.Handle("GET /design/maps/new", auth(getMapNewModal(d)))
	mux.Handle("POST /design/maps", auth(postMapCreate(d)))
	mux.Handle("GET /design/maps/{id}", auth(getMapmakerPage(d)))
	mux.Handle("POST /design/maps/{id}/preview", auth(postMapPreview(d)))
	mux.Handle("POST /design/maps/{id}/materialize", auth(postMapMaterialize(d)))
	mux.Handle("DELETE /design/maps/{id}", auth(deleteMap(d)))
	mux.Handle("GET /design/maps/{id}/settings", auth(getMapSettingsModal(d)))
	mux.Handle("POST /design/maps/{id}/draft", auth(postMapDraft(d)))
	mux.Handle("POST /design/maps/{id}/public-toggle", auth(postMapPublicToggle(d)))
	mux.Handle("POST /design/maps/{mapID}/layers/{layerID}/y-sort", auth(postMapLayerYSortToggle(d)))

	// Per-realm HUD editor (PLAN.md research §P1 #7 + Todo 5).
	// Edits land on maps.hud_layout_json directly (no draft staging
	// for v1 — same pattern as map_tiles and lighting cells).
	mux.Handle("GET /design/maps/{id}/hud", auth(getMapHUDPage(d)))
	mux.Handle("POST /design/maps/{id}/hud/widgets", auth(postHUDWidgetAdd(d)))
	mux.Handle("GET /design/maps/{id}/hud/widgets/{anchor}/{order}", auth(getHUDWidgetForm(d)))
	mux.Handle("POST /design/maps/{id}/hud/widgets/{anchor}/{order}", auth(postHUDWidgetSave(d)))
	mux.Handle("DELETE /design/maps/{id}/hud/widgets/{anchor}/{order}", auth(deleteHUDWidget(d)))
	mux.Handle("POST /design/maps/{id}/hud/widgets/{anchor}/{order}/move", auth(postHUDWidgetMove(d)))
	mux.Handle("POST /design/maps/{id}/hud/anchors/{anchor}", auth(postHUDStackMetadata(d)))

	// Authored-mode painting endpoints. JSON in / out. The design
	// console's mapmaker JS pumps brush strokes through these; the
	// runtime still loads via the same map_tiles rows on next sandbox
	// or push-to-live (no WS required for the editor view itself).
	mux.Handle("GET /design/maps/{id}/tiles", auth(getMapTiles(d)))
	mux.Handle("POST /design/maps/{id}/tiles", auth(postMapTiles(d)))
	mux.Handle("DELETE /design/maps/{id}/tiles", auth(deleteMapTiles(d)))

	// Push-to-Live (PLAN.md §132 + §134). The preview returns the
	// diffs that WOULD land if the user confirmed the push; the
	// post commits the publish + fires the post-commit hooks.
	mux.Handle("GET /design/publish/preview", auth(getPublishPreview(d)))
	mux.Handle("POST /design/publish", auth(postPublish(d)))

	// Sandbox (PLAN.md §131). Map picker + game-view shell pre-
	// configured with a sandbox: instance id + designer WS ticket.
	mux.Handle("GET /design/sandbox", auth(getSandboxIndex(d)))
	mux.Handle("GET /design/sandbox/launch/{id}", auth(getSandboxLaunch(d)))

	// Shared ref pickers (PLAN.md §5d). One generic "choose an asset /
	// entity" modal that any form's KindAssetRef / KindEntityTypeRef
	// field opens via HTMX. Tag filters in the querystring narrow the
	// list (e.g. tags=sprite for a sprite-only field).
	mux.Handle("GET /design/picker/assets", auth(getPickerAssets(d)))
	mux.Handle("GET /design/picker/entities", auth(getPickerEntities(d)))
	// Bulk name lookup so refField can replace "currently #5" with
	// "asset name (#5)" without one fetch per field. boot.js batches
	// every visible refField with a non-zero id into a single call.
	mux.Handle("GET /design/picker/lookup", auth(getPickerLookup(d)))

	// Settings (PLAN.md §5g + §6h). Page is rendered server-side via
	// Templ; the client uses the GET/PUT JSON endpoints to sync. The
	// resolver pulls (designer, designer_id) from the auth context.
	mux.Handle("GET /design/settings", auth(getSettingsPage(d)))
	if d.Settings != nil {
		settingsHandlers := &settings.HTTPHandlers{
			Service: d.Settings,
			Resolver: func(r *http.Request) (settings.Realm, int64, bool) {
				ds := CurrentDesigner(r.Context())
				if ds == nil {
					return "", 0, false
				}
				return settings.RealmDesigner, ds.ID, true
			},
		}
		mux.Handle("GET /design/settings/me", auth(settingsHandlers.Get))
		mux.Handle("PUT /design/settings/me", auth(settingsHandlers.Put))
	}

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

// getShellHome serves the post-login Workspace Home — the design
// console's front door. Aggregates project-level health stats so the
// designer always lands somewhere actionable. Other /design/* surfaces
// (assets, entities, ...) get their own routes; this also catches
// /design/{anything-not-mapped} and shows the home dashboard.
func getShellHome(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		layout := BuildChrome(r, d)
		layout.Title = "Workspace"
		layout.Surface = "shell-home"
		layout.ActiveKind = "home"
		layout.Variant = "no-rail"
		// The home dashboard's "rail" content is the build-steps grid
		// in the body, so we drop the right rail entirely.

		// Cheap project-health stats. The full implementation will
		// pull these from purpose-built queries; for now we derive
		// from the existing tree data so the page is useful right
		// away without new infrastructure.
		props := views.ShellProps{Layout: layout}
		props.EntitiesNoSpr = countEntityWarns(layout.Tree.Entities)
		// Orphans / EmptyMaps stay 0 until those connection lookups
		// land in the next chunk.
		renderHTML(w, r, views.ShellHome(props))
	}
}

// countEntityWarns returns the number of entity-tree items flagged with a
// warning string ("no sprite", etc.). Cheap derive from the chrome's
// already-loaded tree so the home dashboard doesn't issue extra queries.
func countEntityWarns(s views.IndexSection) int {
	n := 0
	for _, it := range s.Items {
		if it.Warn != "" {
			n++
		}
	}
	return n
}

// getSandboxIndex renders the sandbox map picker. PLAN.md §131.
//
// In the design console, sandbox is also launchable inline from any map
// card on /design/maps — this index page remains as the dedicated entry
// point and cross-map test runner.
func getSandboxIndex(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := d.Maps.List(r.Context(), "")
		if err != nil {
			http.Error(w, "list maps: "+err.Error(), http.StatusInternalServerError)
			return
		}
		layout := BuildChrome(r, d)
		layout.Title = "Sandbox"
		layout.Surface = "sandbox-picker"
		layout.ActiveKind = "sandbox"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Sandbox"}}
		renderHTML(w, r, views.SandboxIndex(views.SandboxIndexProps{
			Layout: layout,
			Items:  items,
		}))
	}
}

// getSandboxLaunch builds the sandbox: instance id + mints a designer
// WS ticket, then renders the game view configured for the sandbox.
//
// The instance id format is "sandbox:<designer_id>:<map_id>". The
// AOI subscription manager refuses player-realm subscribers to that
// id space, so the sandbox stays designer-private (PLAN.md §1, §129).
func getSandboxLaunch(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		idStr := r.PathValue("id")
		mapID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || mapID <= 0 {
			http.NotFound(w, r)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), mapID)
		if err != nil {
			if errors.Is(err, mapsservice.ErrMapNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "find map: "+err.Error(), http.StatusInternalServerError)
			return
		}
		ip := clientIP(r)
		ticket, err := d.Auth.MintWSTicket(r.Context(), dr.ID, ip)
		if err != nil {
			http.Error(w, "mint ticket: "+err.Error(), http.StatusInternalServerError)
			return
		}
		instanceID := fmt.Sprintf("sandbox:%d:%d", dr.ID, m.ID)
		renderHTML(w, r, views.SandboxGamePage(views.SandboxGameProps{
			DesignerName: dr.Email,
			Map:          *m,
			WSURL:        resolveSandboxWSURL(r),
			WSTicket:     ticket,
			InstanceID:   instanceID,
		}))
	}
}

func resolveSandboxWSURL(r *http.Request) string {
	scheme := "ws"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	return scheme + "://" + r.Host + "/ws"
}

// getSettingsPage renders the Settings page. Client TS module hydrates
// it from /design/settings/me + drives the live preview, rebinder, etc.
func getSettingsPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		layout := BuildChrome(r, d)
		layout.Title = "Settings"
		layout.Surface = "settings"
		layout.ActiveKind = "settings"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Settings"}}
		renderHTML(w, r, views.SettingsPage(views.SettingsProps{
			Layout:  layout,
			Realm:   "designer",
			LoadURL: "/design/settings/me",
			SaveURL: "/design/settings/me",
		}))
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
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := d.Assets.List(r.Context(), opts)
		if err != nil {
			slog.Error("assets list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		layout := BuildChrome(r, d)
		layout.Title = "Assets"
		layout.Surface = "asset-manager"
		layout.ActiveKind = "asset"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Assets"}}

		// Compute the asset → entity-count map so orphan/used-by badges
		// render with real numbers. Failure degrades gracefully (badges
		// fall back to "—").
		usage, err := AssetUsageMap(r.Context(), d, assetIDs(items))
		if err != nil {
			slog.Warn("asset usage map", "err", err)
		}

		items = applyAssetFilter(items, filter, usage)

		renderHTML(w, r, views.AssetsList(views.AssetsListProps{
			Layout:       layout,
			Items:        items,
			ActiveKind:   string(opts.Kind),
			Search:       opts.Search,
			PublicURL:    assetPublicURLFunc(items),
			UsageByID:    usage,
			ActiveFilter: filter,
		}))
	}
}

// getAssetBlob serves designer-visible asset bytes from the same origin
// as the design tool. This keeps previews and the Mapmaker canvas out of
// MinIO/CDN CORS trouble while still requiring the designer session.
func getAssetBlob(d Deps) http.HandlerFunc {
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
		body, err := d.ObjectStore.Get(r.Context(), a.ContentAddressedPath)
		if err != nil {
			slog.Warn("asset blob", "err", err, "id", id)
			http.Error(w, "asset unavailable", http.StatusBadGateway)
			return
		}
		defer body.Close()

		w.Header().Set("Cache-Control", "private, max-age=300")
		w.Header().Set("Content-Type", contentTypeForAsset(*a))
		_, _ = io.Copy(w, body)
	}
}

// applyAssetFilter narrows the asset list to those matching the
// ?filter= scope. "orphan" keeps assets with zero references; unknown
// values are ignored (returns the input slice untouched). The chrome
// badge in the toolbar advertises the active filter so the trim is
// visible.
func applyAssetFilter(items []assets.Asset, filter string, usage map[int64]int) []assets.Asset {
	if filter == "" || usage == nil {
		return items
	}
	switch filter {
	case "orphan":
		out := make([]assets.Asset, 0, len(items))
		for _, a := range items {
			if usage[a.ID] == 0 {
				out = append(out, a)
			}
		}
		return out
	default:
		return items
	}
}

// assetIDs slices out just the IDs from an asset list — used to feed
// AssetUsageMap without copying full asset rows.
func assetIDs(items []assets.Asset) []int64 {
	out := make([]int64, len(items))
	for i, a := range items {
		out[i] = a.ID
	}
	return out
}

func assetPublicURLFunc(items []assets.Asset) func(string) string {
	byPath := make(map[string]int64, len(items))
	for _, a := range items {
		byPath[a.ContentAddressedPath] = a.ID
	}
	return func(path string) string {
		if id, ok := byPath[path]; ok && id > 0 {
			return "/design/assets/blob/" + strconv.FormatInt(id, 10)
		}
		return ""
	}
}

func mapsValues[K comparable, V any](m map[K]V) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func contentTypeForAsset(a assets.Asset) string {
	switch a.Kind {
	case assets.KindAudio:
		format := strings.ToLower(strings.TrimPrefix(a.OriginalFormat, "."))
		switch format {
		case "ogg", "oga":
			return "audio/ogg"
		case "mp3", "mpeg":
			return "audio/mpeg"
		case "wav":
			return "audio/wav"
		default:
			return "application/octet-stream"
		}
	default:
		if strings.EqualFold(a.OriginalFormat, "svg") {
			return "image/svg+xml"
		}
		return "image/png"
	}
}

// getAssetsGrid returns just the inner grid HTML for HTMX swaps from the
// search/filter form. No chrome data is sent — just the work area's
// grid contents.
func getAssetsGrid(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := assetListOptsFromQuery(r)
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := d.Assets.List(r.Context(), opts)
		if err != nil {
			slog.Error("assets list (grid)", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		usage, _ := AssetUsageMap(r.Context(), d, assetIDs(items))
		items = applyAssetFilter(items, filter, usage)
		renderHTML(w, r, views.AssetsGrid(views.AssetsListProps{
			Items:        items,
			ActiveKind:   string(opts.Kind),
			Search:       opts.Search,
			PublicURL:    assetPublicURLFunc(items),
			UsageByID:    usage,
			ActiveFilter: filter,
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

// getAssetDetail renders the per-asset modal. Loads the connections
// rail data inline so the modal carries "used by" context without an
// extra round trip.
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
		var usedBy []views.RailRef
		if rail, err := ConnectionsForAsset(r.Context(), d, id); err == nil && rail != nil {
			usedBy = rail.UsedBy
		} else if err != nil {
			slog.Warn("connections for asset", "err", err, "id", id)
		}
		renderHTML(w, r, views.AssetDetail(views.AssetDetailProps{
			Asset:     *a,
			PublicURL: assetPublicURLFunc([]assets.Asset{*a}),
			UsedBy:    usedBy,
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
			PublicURL: assetPublicURLFunc(items),
		}))
	}
}

// postAssetPromoteToEntity creates a fresh entity_type that uses this
// asset as its sprite, then redirects the HTMX caller to open the new
// entity's detail modal. One-click "I have a sprite — give me an entity
// I can paint with."
//
// Tile-kind assets get tagged "tile" so the Mapmaker palette filter
// surfaces them right away. Sprite-kind assets are left untagged so
// they don't pollute the tile palette by default.
//
// Name conflicts walk a "Name (copy N)" suffix the same way
// postEntityDuplicate does.
func postAssetPromoteToEntity(d Deps) http.HandlerFunc {
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
		a, err := d.Assets.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		}
		if a.Kind == assets.KindAudio {
			http.Error(w, "audio assets can't be promoted to an entity (no sprite)", http.StatusBadRequest)
			return
		}

		// Suffix-walk to a free name. Most users will have one entity
		// per asset; the loop covers re-promotion of the same asset.
		baseName := a.Name
		newName := baseName
		for i := 2; i < 100; i++ {
			if _, err := d.Entities.FindByName(r.Context(), newName); errors.Is(err, entities.ErrEntityTypeNotFound) {
				break
			}
			newName = fmt.Sprintf("%s (%d)", baseName, i)
		}

		assetID := a.ID
		var tags []string
		if a.Kind == assets.KindTile {
			tags = []string{"tile"}
		}

		et, err := d.Entities.Create(r.Context(), entities.CreateInput{
			Name:          newName,
			SpriteAssetID: &assetID,
			Tags:          tags,
			CreatedBy:     dr.ID,
		})
		if err != nil {
			slog.Error("promote-to-entity", "err", err, "asset_id", id)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Redirect the HTMX caller to the new entity's detail modal.
		// HX-Redirect navigates the page; HX-Trigger fires a custom
		// event the asset modal can use to close itself first.
		w.Header().Set("HX-Trigger", "bx:asset-promoted")
		w.Header().Set("HX-Redirect", fmt.Sprintf("/design/entities/%d", et.ID))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fmt.Sprintf(
			`<div class="bx-toast bx-toast--success">Created entity <strong>%s</strong>. Opening editor…</div>`,
			templHTMLEscape(newName),
		)))
	}
}

// postAssetPromoteBulk creates a tile-tagged entity for each asset id
// in the ?ids= querystring and re-renders the AssetUploadResults
// fragment with Promoted=true on success rows. Drives both the per-row
// "+ Entity" affordance (single id) and the "+ Tile entity from each"
// footer (comma-joined ids) without two endpoints.
//
// On error per id, the row stays not-promoted so the user can retry;
// other rows still complete. The response is always the refreshed
// fragment so HTMX outerHTML swaps replace the whole result list.
func postAssetPromoteBulk(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		raw := r.URL.Query().Get("ids")
		if raw == "" {
			if err := r.ParseForm(); err == nil {
				raw = r.FormValue("ids")
			}
		}
		ids := parseCommaIDs(raw)
		if len(ids) == 0 {
			http.Error(w, "no ids", http.StatusBadRequest)
			return
		}

		items := make([]views.AssetUploadItem, 0, len(ids))
		ok := 0
		for _, id := range ids {
			a, err := d.Assets.FindByID(r.Context(), id)
			if err != nil {
				items = append(items, views.AssetUploadItem{
					AssetID:    id,
					OriginalFn: fmt.Sprintf("asset #%d", id),
					Err:        "asset not found",
				})
				continue
			}
			item := views.AssetUploadItem{
				AssetID:    a.ID,
				Name:       a.Name,
				Kind:       string(a.Kind),
				OriginalFn: a.Name,
			}
			if a.Kind == assets.KindAudio {
				// Audio can't be promoted (no sprite). Surface the
				// row but mark it as already in the library so the
				// user sees why nothing happened.
				item.Reused = true
				items = append(items, item)
				continue
			}
			if _, err := promoteAssetToEntity(r.Context(), d, a, dr.ID); err != nil {
				item.Err = err.Error()
			} else {
				item.Promoted = true
				ok++
			}
			items = append(items, item)
		}
		renderHTML(w, r, views.AssetUploadResults(views.AssetUploadResultsProps{
			Items:    items,
			OKCount:  ok,
			ErrCount: len(items) - ok,
		}))
	}
}

// promoteAssetToEntity creates a tile-tagged entity for the asset and
// returns it. Idempotent-ish: if an entity with the same name already
// exists, walks the "(N)" suffix the same way the single-id endpoint
// does. Pulled out so postAssetPromoteToEntity, postAssetPromoteBulk,
// and the upload-time promote-after-upload path all share the rules.
//
// Used for SPRITE assets (single-cell). Tile sheets go through
// autoSliceTileSheet so each non-empty 32x32 cell gets its own
// entity_type with the right atlas_index — the only way the Mapmaker
// palette can render real tile artwork instead of a yellow #1213
// chip.
func promoteAssetToEntity(ctx context.Context, d Deps, a *assets.Asset, designerID int64) (*entities.EntityType, error) {
	baseName := a.Name
	newName := baseName
	for i := 2; i < 100; i++ {
		if _, err := d.Entities.FindByName(ctx, newName); errors.Is(err, entities.ErrEntityTypeNotFound) {
			break
		}
		newName = fmt.Sprintf("%s (%d)", baseName, i)
	}
	assetID := a.ID
	tags := []string{"tile"} // bulk + upload-time promote always tag as tile
	return d.Entities.Create(ctx, entities.CreateInput{
		Name:          newName,
		SpriteAssetID: &assetID,
		AtlasIndex:    0,
		Tags:          tags,
		CreatedBy:     designerID,
	})
}

// autoSliceTileSheet creates one tile-tagged entity_type per non-empty
// 32x32 cell of a freshly uploaded tile sheet. Idempotent against the
// existing (sprite_asset_id, atlas_index) set so re-uploads or
// re-slices don't pile up duplicate palette entries.
//
// Returns the number of entities ACTUALLY created (skipped + reused
// cells don't count). Errors per-cell are logged and skipped — the
// rest of the sheet still slices, so a partial failure leaves the
// designer with a usable palette instead of nothing.
//
// Naming: "<asset name> #r{R}c{C}" — the column/row coordinates are
// what designers use when tiles are arranged spatially in their
// source app (Aseprite, Tiled, etc.), so it's the most legible
// label for finding a specific cell in the palette.
func autoSliceTileSheet(
	ctx context.Context,
	d Deps,
	a *assets.Asset,
	cells []assets.TileCell,
	designerID int64,
) (int, error) {
	if d.Entities == nil {
		return 0, errors.New("entities service not configured")
	}
	existing, err := d.Entities.FindBySpriteAtlas(ctx, a.ID)
	if err != nil {
		return 0, fmt.Errorf("lookup existing tile entities: %w", err)
	}
	have := make(map[int32]struct{}, len(existing))
	for _, et := range existing {
		have[et.AtlasIndex] = struct{}{}
	}

	assetID := a.ID
	created := 0
	for _, cell := range cells {
		if !cell.NonEmpty {
			continue
		}
		if _, ok := have[int32(cell.Index)]; ok {
			continue
		}
		name := fmt.Sprintf("%s #r%dc%d", a.Name, cell.Row, cell.Col)
		// Name uniqueness across the whole project is enforced by the
		// entity_types_name_key constraint; bump the suffix on
		// collision (e.g. two sheets with the same base name).
		uniqueName := name
		for i := 2; i < 100; i++ {
			if _, ferr := d.Entities.FindByName(ctx, uniqueName); errors.Is(ferr, entities.ErrEntityTypeNotFound) {
				break
			}
			uniqueName = fmt.Sprintf("%s (%d)", name, i)
		}
		if _, cerr := d.Entities.Create(ctx, entities.CreateInput{
			Name:          uniqueName,
			SpriteAssetID: &assetID,
			AtlasIndex:    int32(cell.Index),
			Tags:          []string{"tile"},
			CreatedBy:     designerID,
		}); cerr != nil {
			slog.Warn("auto-slice cell create",
				"err", cerr,
				"asset_id", a.ID,
				"atlas_index", cell.Index,
			)
			continue
		}
		created++
	}
	return created, nil
}

// postAssetDeleteBulk deletes a list of assets and re-renders the
// asset grid in place. Bound to 256 ids by parseCommaIDs so a
// runaway request can't fan out an unbounded delete.
//
// Per-id failures are tolerated (already-gone assets, etc.); the
// endpoint always returns the refreshed grid so the user sees what
// actually happened.
func postAssetDeleteBulk(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ids := parseCommaIDs(firstNonEmpty(r.URL.Query().Get("ids"), r.FormValue("ids")))
		if len(ids) == 0 {
			http.Error(w, "no ids", http.StatusBadRequest)
			return
		}
		for _, id := range ids {
			if err := d.Assets.Delete(r.Context(), id); err != nil {
				if errors.Is(err, assets.ErrAssetNotFound) {
					continue
				}
				slog.Warn("bulk asset delete", "err", err, "id", id)
			}
		}
		items, _ := d.Assets.List(r.Context(), assets.ListOpts{})
		usage, _ := AssetUsageMap(r.Context(), d, assetIDs(items))
		renderHTML(w, r, views.AssetsGrid(views.AssetsListProps{
			Items:     items,
			PublicURL: assetPublicURLFunc(items),
			UsageByID: usage,
		}))
	}
}

// postEntityDeleteBulk deletes a list of entity types and re-renders
// the entities grid in place. Mirrors postAssetDeleteBulk's tolerance
// for per-id failures.
func postEntityDeleteBulk(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ids := parseCommaIDs(firstNonEmpty(r.URL.Query().Get("ids"), r.FormValue("ids")))
		if len(ids) == 0 {
			http.Error(w, "no ids", http.StatusBadRequest)
			return
		}
		for _, id := range ids {
			if err := d.Entities.Delete(r.Context(), id); err != nil {
				if errors.Is(err, entities.ErrEntityTypeNotFound) {
					continue
				}
				slog.Warn("bulk entity delete", "err", err, "id", id)
			}
		}
		items, _ := d.Entities.List(r.Context(), entities.ListOpts{})
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{Items: items}))
	}
}

// postEntityTagBulk adds or removes a tag on a list of entity types
// (the bulk-select "Add tile tag" affordance is the v1 driver, but the
// endpoint is generic so other tag operations can ride on it without a
// schema change). Form fields:
//
//	ids=1,2,3   comma-joined entity ids (or repeated query params)
//	tag=tile    the tag to add/remove
//	op=add      "add" (default) or "remove"
//
// Returns the refreshed grid HTML so HTMX outerHTML swaps re-render in
// place.
func postEntityTagBulk(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ids := parseCommaIDs(firstNonEmpty(r.URL.Query().Get("ids"), r.FormValue("ids")))
		if len(ids) == 0 {
			http.Error(w, "no ids", http.StatusBadRequest)
			return
		}
		tag := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("tag"), r.FormValue("tag")))
		if tag == "" {
			http.Error(w, "tag is required", http.StatusBadRequest)
			return
		}
		op := strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("op"), r.FormValue("op")))
		if op == "" {
			op = "add"
		}
		for _, id := range ids {
			et, err := d.Entities.FindByID(r.Context(), id)
			if err != nil {
				slog.Warn("bulk tag: find entity", "err", err, "id", id)
				continue
			}
			next := applyTagOp(et.Tags, tag, op)
			// SQL update direct so we don't roundtrip the full draft
			// pipeline for a single-tag edit. The entity is already
			// persisted; we're patching one column.
			if _, err := d.Entities.Pool.Exec(r.Context(),
				`UPDATE entity_types SET tags = $2 WHERE id = $1`,
				id, next,
			); err != nil {
				slog.Warn("bulk tag: update", "err", err, "id", id)
			}
		}
		items, _ := d.Entities.List(r.Context(), entities.ListOpts{})
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{Items: items}))
	}
}

// applyTagOp returns the tag slice with `tag` either appended (if "add"
// and not already present) or removed (if "remove"). Order is
// preserved so the UI list doesn't reshuffle.
func applyTagOp(tags []string, tag, op string) []string {
	switch op {
	case "remove":
		out := make([]string, 0, len(tags))
		for _, t := range tags {
			if t != tag {
				out = append(out, t)
			}
		}
		return out
	default: // add
		for _, t := range tags {
			if t == tag {
				return tags
			}
		}
		return append(append([]string{}, tags...), tag)
	}
}

// firstNonEmpty returns the first non-empty argument. Used by the
// bulk endpoints to accept ids/tag/op via either query string or form
// body so HTMX callers can pick whichever is most ergonomic.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseCommaIDs splits "1,2,3" or repeated "?ids=1&ids=2" semantics
// into a deduplicated int64 slice. Bound to 256 ids so a runaway
// request can't fan out unbounded entity creates.
func parseCommaIDs(raw string) []int64 {
	const max = 256
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > max {
		parts = parts[:max]
	}
	out := make([]int64, 0, len(parts))
	seen := map[int64]struct{}{}
	for _, p := range parts {
		n, err := strconvAtoi64(strings.TrimSpace(p))
		if err != nil || n <= 0 {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// templHTMLEscape escapes the few characters that matter inside a toast
// payload. Templ does this automatically inside templates; here we're
// emitting a literal byte string so we escape ourselves to keep the
// payload safe for entity names containing < > & " '.
func templHTMLEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&#34;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
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
		writeDraftSavedToast(w, "asset")
	}
}

// writeDraftSavedToast emits the canonical "draft saved" toast. The
// markup is verbose (it nudges users toward Push to Live) but tagged
// `data-bx-draft-toast` so boot.js can shrink it to a quiet "Draft
// saved" once the user has seen the verbose version once. The chrome's
// running draft-count pill is the persistent surface for that
// affordance after that.
func writeDraftSavedToast(w http.ResponseWriter, kind string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(
		`<div class="bx-toast bx-toast--success" data-bx-draft-toast data-copy-slot="` + kind + `.draft.saved">` +
			`<span data-bx-draft-toast-verbose>` +
			draftToastVerboseFor(kind) +
			` <a href="#" hx-get="/design/publish/preview" hx-target="#publish-modal-host" hx-swap="innerHTML" style="text-decoration: underline;">Push to Live</a> ` +
			draftToastTailFor(kind) +
			`</span>` +
			`<span data-bx-draft-toast-short hidden>Draft saved.</span>` +
			`</div>`,
	))
}

// draftToastVerboseFor returns the verbose lead-in line used the first
// time a designer sees the draft toast for a given artifact kind.
// Tail is supplied by draftToastTailFor so kinds can describe their
// own consequence (e.g. "to apply changes to the Mapmaker palette").
func draftToastVerboseFor(kind string) string {
	switch kind {
	case "entity":
		return `Draft saved.`
	case "map":
		return `Map draft saved.`
	default:
		return `Draft saved.`
	}
}

func draftToastTailFor(kind string) string {
	switch kind {
	case "entity":
		return `to apply changes to the Mapmaker palette and live game.`
	case "map":
		return `to make changes visible to players.`
	default:
		return `when you're ready to publish.`
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
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
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
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := d.Entities.List(r.Context(), opts)
		if err != nil {
			slog.Error("entities list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items = applyEntityFilter(items, filter)
		layout := BuildChrome(r, d)
		layout.Title = "Entities"
		layout.Surface = "entity-manager"
		layout.ActiveKind = "entity"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Entities"}}
		renderHTML(w, r, views.EntitiesList(views.EntitiesListProps{
			Layout:       layout,
			Items:        items,
			Search:       opts.Search,
			ActiveFilter: filter,
		}))
	}
}

func getEntitiesGrid(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := entityListOptsFromQuery(r)
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := d.Entities.List(r.Context(), opts)
		if err != nil {
			slog.Error("entities grid", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items = applyEntityFilter(items, filter)
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{
			Items:        items,
			Search:       opts.Search,
			ActiveFilter: filter,
		}))
	}
}

// applyEntityFilter narrows the entity list to those matching the
// ?filter= scope. "no-sprite" keeps entities missing a sprite asset;
// unknown values are no-ops.
func applyEntityFilter(items []entities.EntityType, filter string) []entities.EntityType {
	if filter == "" {
		return items
	}
	switch filter {
	case "no-sprite":
		out := make([]entities.EntityType, 0, len(items))
		for _, et := range items {
			if et.SpriteAssetID == nil {
				out = append(out, et)
			}
		}
		return out
	default:
		return items
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
		et, err := d.Entities.Create(r.Context(), entities.CreateInput{
			Name:      name,
			CreatedBy: dr.ID,
		})
		if err != nil {
			if errors.Is(err, entities.ErrNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("entity create", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Same pattern as postMapCreate: from /design/entities, swap
		// the grid; from anywhere else (home, chrome, deep link),
		// open the freshly-created entity's detail modal so the user
		// can immediately assign a sprite + components without
		// hunting for the new row.
		if !cameFromEntitiesList(r) && et != nil {
			w.Header().Set("HX-Redirect", fmt.Sprintf("/design/entities/%d", et.ID))
			w.WriteHeader(http.StatusOK)
			return
		}
		items, _ := d.Entities.List(r.Context(), entities.ListOpts{})
		renderHTML(w, r, views.EntitiesGrid(views.EntitiesListProps{Items: items}))
	}
}

// cameFromEntitiesList returns true when the HTMX request originated
// on the /design/entities list page. Mirrors cameFromMapsList — when
// HX-Current-URL is absent (non-HTMX caller / test client), default
// to "from list" so the old grid-returning behavior is preserved.
func cameFromEntitiesList(r *http.Request) bool {
	cur := r.Header.Get("HX-Current-URL")
	if cur == "" {
		return true
	}
	return strings.Contains(cur, "/design/entities") &&
		!strings.Contains(cur, "/design/entities/")
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

		// Connections rail data so the modal carries the "used by N maps"
		// count needed to build a non-lying delete confirm. Soft-fails
		// to an empty list rather than blocking the page render.
		if rail, err := ConnectionsForEntity(r.Context(), d, id); err == nil && rail != nil {
			props.UsedBy = rail.UsedBy
		} else if err != nil {
			slog.Warn("connections for entity", "err", err, "id", id)
		}

		// Wire automations if the service is available. Read failures
		// degrade gracefully — missing automations just render the
		// editor empty.
		if d.Automations != nil {
			if set, err := d.Automations.Get(r.Context(), id); err == nil {
				props.Automations = set
			} else {
				slog.Warn("automations get for detail", "err", err, "entity_id", id)
			}
			props.AutomationTriggers = d.AutomationTriggers
			props.AutomationActions = d.AutomationActions
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
		writeDraftSavedToast(w, "entity")
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

// parseCheckbox returns true for the values an HTML checkbox can submit
// when it's checked: "on" (default), "true", "1". An unchecked checkbox
// sends nothing, so the empty string is false.
func parseCheckbox(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "true", "1", "yes":
		return true
	default:
		return false
	}
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
	return assetPublicURLFunc([]assets.Asset{*a})(a.ContentAddressedPath)
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
		usage, err := SocketUsageMap(r.Context(), d)
		if err != nil {
			slog.Warn("socket usage map", "err", err)
			usage = nil // templ falls back to neutral "—"
		}
		layout := BuildChrome(r, d)
		layout.Title = "Sockets"
		layout.Surface = "edge-sockets"
		layout.ActiveKind = "socket"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Sockets"}}
		renderHTML(w, r, views.SocketsList(views.SocketsListProps{
			Layout:    layout,
			Items:     items,
			UsageByID: usage,
		}))
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
		usage, _ := SocketUsageMap(r.Context(), d)
		renderHTML(w, r, views.SocketsGrid(views.SocketsListProps{Items: items, UsageByID: usage}))
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
		usage, _ := SocketUsageMap(r.Context(), d)
		renderHTML(w, r, views.SocketsGrid(views.SocketsListProps{Items: items, UsageByID: usage}))
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
		layout := BuildChrome(r, d)
		layout.Title = "Tile groups"
		layout.Surface = "tile-groups"
		layout.ActiveKind = "group"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Tile groups"}}
		renderHTML(w, r, views.TileGroupsList(views.TileGroupsListProps{
			Layout: layout,
			Items:  items,
		}))
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
		if err := d.Entities.UpdateTileGroupLayoutAndProcedural(r.Context(), id, entities.UpdateTileGroupLayoutInput{
			Layout:                       layout,
			ExcludeMembersFromProcedural: r.FormValue("exclude_members_from_procedural") == "on",
			UseGroupInProcedural:         r.FormValue("use_group_in_procedural") == "on",
		}); err != nil {
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

// ---- Mapmaker ----

func getMapsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := d.Maps.List(r.Context(), search)
		if err != nil {
			slog.Error("maps list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items = applyMapFilter(r, d, items, filter)
		layout := BuildChrome(r, d)
		layout.Title = "Maps"
		layout.Surface = "maps"
		layout.ActiveKind = "map"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Maps"}}
		renderHTML(w, r, views.MapsList(views.MapsListProps{
			Layout:       layout,
			Items:        items,
			Search:       search,
			ActiveFilter: filter,
		}))
	}
}

func getMapsGrid(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := d.Maps.List(r.Context(), search)
		if err != nil {
			slog.Error("maps grid", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items = applyMapFilter(r, d, items, filter)
		renderHTML(w, r, views.MapsGrid(views.MapsListProps{Items: items, Search: search, ActiveFilter: filter}))
	}
}

// applyMapFilter narrows the maps list to those matching ?filter=.
// "empty" runs a single GROUP BY query against map_tiles to find
// every map with at least one placement, then keeps the complement.
// Soft-fails on query error: returns the input slice + a warning log
// so a transient DB hiccup never empties the list.
func applyMapFilter(r *http.Request, d Deps, items []mapsservice.Map, filter string) []mapsservice.Map {
	if filter == "" {
		return items
	}
	switch filter {
	case "empty":
		populated := map[int64]bool{}
		rows, err := d.Maps.Pool.Query(r.Context(),
			`SELECT DISTINCT map_id FROM map_tiles`)
		if err != nil {
			slog.Warn("maps filter: query populated maps", "err", err)
			return items
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				slog.Warn("maps filter: scan id", "err", err)
				return items
			}
			populated[id] = true
		}
		out := make([]mapsservice.Map, 0, len(items))
		for _, m := range items {
			if !populated[m.ID] {
				out = append(out, m)
			}
		}
		return out
	default:
		return items
	}
}

// getMapNewModal renders the create-new-map dialog. Reuses the generic
// form renderer + MapDraft.Descriptor() so adding a future field on
// MapDraft updates this modal automatically.
func getMapNewModal(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Custom small-form HTML rather than the full Form partial because
		// the create-new path needs width + height which aren't in
		// MapDraft.Descriptor() (those are immutable post-create).
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<div class="bx-modal-backdrop" data-bx-dismissible role="dialog" aria-modal="true">
  <div class="bx-modal">
    <header class="bx-modal__header">
      <h2 data-copy-slot="maps.new.title">New map</h2>
      <button type="button" class="bx-btn bx-btn--ghost"
              hx-on:click="this.closest('.bx-modal-backdrop').remove()"
              aria-label="Close">Esc</button>
    </header>
    <div class="bx-modal__body">
      <form hx-post="/design/maps" hx-target="#maps-grid" hx-swap="outerHTML"
            hx-on:htmx:after-request="this.closest('.bx-modal-backdrop')?.remove()"
            class="bx-stack">
        <div class="bx-field">
          <label for="m-name" class="bx-label">Name</label>
          <input id="m-name" name="name" class="bx-input" required maxlength="128" autofocus>
        </div>
        <div class="bx-row">
          <div class="bx-field" style="flex: 1;">
            <label for="m-width" class="bx-label">Width (tiles)</label>
            <input id="m-width" name="width" type="number" class="bx-input" min="1" value="64">
          </div>
          <div class="bx-field" style="flex: 1;">
            <label for="m-height" class="bx-label">Height (tiles)</label>
            <input id="m-height" name="height" type="number" class="bx-input" min="1" value="48">
          </div>
        </div>
        <div class="bx-field">
          <label for="m-mode" class="bx-label">Mode</label>
          <select id="m-mode" name="mode" class="bx-select">
            <option value="authored">Authored</option>
            <option value="procedural">Procedural (WFC)</option>
          </select>
        </div>
        <div class="bx-field">
          <label class="bx-row" for="m-public" style="gap: var(--bx-s2); align-items: center;">
            <input id="m-public" name="public" type="checkbox" value="true" class="bx-checkbox">
            <span class="bx-label" style="margin: 0;">Public — players can see this map at /play/maps</span>
          </label>
          <small class="bx-help">Off = sandbox-only (designers can still test it). Toggle later from the Mapmaker header.</small>
        </div>
        <div class="bx-row bx-row--end">
          <button type="submit" class="bx-btn bx-btn--primary">Create</button>
        </div>
      </form>
    </div>
  </div>
</div>
`))
	}
}

func postMapCreate(d Deps) http.HandlerFunc {
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
		m, err := d.Maps.Create(r.Context(), mapsservice.CreateInput{
			Name:      r.FormValue("name"),
			Width:     int32(parseIntOr(r.FormValue("width"), 64)),
			Height:    int32(parseIntOr(r.FormValue("height"), 48)),
			Mode:      strings.TrimSpace(r.FormValue("mode")),
			Public:    parseCheckbox(r.FormValue("public")),
			CreatedBy: dr.ID,
		})
		if err != nil {
			if errors.Is(err, mapsservice.ErrNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("map create", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// If the request came from /design/maps the caller's HX-target
		// swap is what they want — refreshes the grid in place. From
		// any other surface (the home page +Map button, or the chrome
		// counts), redirect straight to the new map's editor so the
		// user lands somewhere they can act on. Detect by HX-Current-URL.
		if !cameFromMapsList(r) && m != nil {
			w.Header().Set("HX-Redirect", fmt.Sprintf("/design/maps/%d", m.ID))
			w.WriteHeader(http.StatusOK)
			return
		}
		items, _ := d.Maps.List(r.Context(), "")
		renderHTML(w, r, views.MapsGrid(views.MapsListProps{Items: items}))
	}
}

// cameFromMapsList returns true when the HTMX request originated on
// the /design/maps list page, where the caller wants its grid swapped
// in place. Any other source with a positively-identified non-list
// surface (home, chrome, deep link) gets the HX-Redirect path to the
// new map's editor. Non-HTMX clients (curl, tests, server-to-server)
// have no HX-Current-URL so we treat them as "from list" — keeps the
// legacy POST-returns-grid behavior intact.
func cameFromMapsList(r *http.Request) bool {
	cur := r.Header.Get("HX-Current-URL")
	if cur == "" {
		return true
	}
	// HX-Current-URL is absolute; we only care about the path suffix.
	// Match /design/maps and /design/maps?... but NOT /design/maps/123.
	return strings.Contains(cur, "/design/maps") &&
		!strings.Contains(cur, "/design/maps/")
}

func getMapmakerPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		layers, err := d.Maps.Layers(r.Context(), id)
		if err != nil {
			slog.Error("map layers", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Build the tile-palette. Prefer entity types tagged "tile" so
		// NPCs / items / triggers don't pollute the brush. Fall back to
		// listing everything if the project hasn't tagged anything yet —
		// otherwise a brand-new designer would see an empty palette and
		// nothing to paint with.
		var palette []views.PaletteEntry
		ets, err := d.Entities.List(r.Context(), entities.ListOpts{Tags: []string{"tile"}, Limit: 200})
		if err == nil && len(ets) == 0 {
			ets, err = d.Entities.List(r.Context(), entities.ListOpts{Limit: 200})
		}
		if err == nil && len(ets) > 0 {
			// Batch-load every referenced sprite asset in one query
			// so the palette build is O(1) DB calls regardless of how
			// many tile entities the project has. (Was N+1.)
			assetIDs := make([]int64, 0, len(ets))
			seen := make(map[int64]struct{}, len(ets))
			for _, et := range ets {
				if et.SpriteAssetID == nil {
					continue
				}
				if _, ok := seen[*et.SpriteAssetID]; ok {
					continue
				}
				seen[*et.SpriteAssetID] = struct{}{}
				assetIDs = append(assetIDs, *et.SpriteAssetID)
			}
			assetByID := make(map[int64]assets.Asset, len(assetIDs))
			if d.Assets != nil && len(assetIDs) > 0 {
				if rows, lerr := d.Assets.ListByIDs(r.Context(), assetIDs); lerr == nil {
					for _, a := range rows {
						assetByID[a.ID] = a
					}
				} else {
					slog.Warn("palette assets bulk lookup", "err", lerr)
				}
			}
			paletteURL := assetPublicURLFunc(mapsValues(assetByID))
			for _, et := range ets {
				entry := views.PaletteEntry{
					ID:         et.ID,
					Name:       et.Name,
					AtlasIndex: et.AtlasIndex,
					TileSize:   assets.TileSize,
					AtlasCols:  1,
				}
				if et.SpriteAssetID != nil {
					if a, ok := assetByID[*et.SpriteAssetID]; ok {
						entry.SpriteURL = paletteURL(a.ContentAddressedPath)
						// Tile sheets carry their grid dims in
						// metadata_json; sprite assets fall through
						// to the (1x1, idx 0) defaults above so the
						// renderer still draws them as a single 32x32
						// cell.
						if md, derr := assets.DecodeTileSheetMetadata(a.MetadataJSON); derr == nil && md.Cols > 0 {
							entry.AtlasCols = int32(md.Cols)
							entry.TileSize = int32(md.TileSize)
						}
					}
				}
				palette = append(palette, entry)
			}
		}
		// Count entity_type drafts so the palette can warn the designer
		// that pending sprite/collider edits aren't visible until they
		// Push to Live. Failure degrades quietly.
		var entityDrafts int
		_ = d.Maps.Pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM drafts WHERE artifact_kind = 'entity_type'`,
		).Scan(&entityDrafts)

		layout := BuildChrome(r, d)
		layout.Title = "Mapmaker · " + m.Name
		layout.Surface = "mapmaker"
		layout.ActiveKind = "map"
		layout.ActiveID = m.ID
		layout.Variant = "bleed"
		layout.BodyClass = "bx-mapmaker-body"
		renderHTML(w, r, views.MapmakerPage(views.MapmakerProps{
			Layout:             layout,
			Map:                *m,
			Layers:             layers,
			PaletteEntityTypes: palette,
			EntityDraftCount:   entityDrafts,
		}))
	}
}

// previewRequest is the JSON body POSTed by the procedural-mode UI.
// All fields are optional except seed (which defaults to 0); the map's
// own width/height are used unless the body overrides them, so the
// designer can preview a smaller region while iterating on the seed.
type previewRequest struct {
	Seed       uint64 `json:"seed"`
	Width      int32  `json:"width,omitempty"`
	Height     int32  `json:"height,omitempty"`
	MaxReseeds int    `json:"max_reseeds,omitempty"`
	Anchors    []struct {
		X            int32 `json:"x"`
		Y            int32 `json:"y"`
		EntityTypeID int64 `json:"entity_type_id"`
	} `json:"anchors,omitempty"`
}

// previewResponse mirrors wfc.Region but uses snake_case keys for the
// JS client. Cells are flat row-major to keep the payload small.
type previewResponse struct {
	Width       int32             `json:"width"`
	Height      int32             `json:"height"`
	TileSetSize int               `json:"tileset_size"`
	Cells       []previewCellJSON `json:"cells"`
}

type previewCellJSON struct {
	X            int32 `json:"x"`
	Y            int32 `json:"y"`
	EntityTypeID int64 `json:"entity_type_id"`
}

// postMapPreview returns a procedural WFC preview for the given map.
// JSON in / JSON out — the Mapmaker JS overlays the result as a ghost
// layer on the canvas. No mutation: the preview is purely read-only.
func postMapPreview(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var req previewRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		width := req.Width
		if width <= 0 {
			width = m.Width
		}
		height := req.Height
		if height <= 0 {
			height = m.Height
		}

		anchors := make([]wfc.Cell, 0, len(req.Anchors))
		for _, a := range req.Anchors {
			anchors = append(anchors, wfc.Cell{
				X:          a.X,
				Y:          a.Y,
				EntityType: wfc.EntityTypeID(a.EntityTypeID),
			})
		}

		res, err := d.Maps.GenerateProceduralPreview(r.Context(), mapsservice.ProceduralPreviewInput{
			Width:      width,
			Height:     height,
			Seed:       req.Seed,
			Anchors:    anchors,
			MaxReseeds: req.MaxReseeds,
		})
		if err != nil {
			switch {
			case errors.Is(err, mapsservice.ErrNoTileKinds):
				http.Error(w, "no tile-kind entity types in project; create some in the Entity Manager first", http.StatusUnprocessableEntity)
			case errors.Is(err, wfc.ErrInvalidRegion):
				http.Error(w, "invalid width/height", http.StatusBadRequest)
			case errors.Is(err, wfc.ErrTooManyReseeds):
				http.Error(w, "wfc could not satisfy constraints; try a different seed or relax anchors", http.StatusUnprocessableEntity)
			default:
				slog.Error("wfc preview", "err", err, "map_id", id)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}

		out := previewResponse{
			Width:       res.Region.Width,
			Height:      res.Region.Height,
			TileSetSize: res.TileSetSize,
			Cells:       make([]previewCellJSON, 0, len(res.Region.Cells)),
		}
		for _, c := range res.Region.Cells {
			out.Cells = append(out.Cells, previewCellJSON{
				X: c.X, Y: c.Y, EntityTypeID: int64(c.EntityType),
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// materializeRequest commits a previewed procedural layout to the
// map_tiles table. Only meaningful for procedural+persistent maps; the
// service rejects other modes.
type materializeRequest struct {
	Seed    uint64 `json:"seed"`
	LayerID int64  `json:"layer_id,omitempty"`
}

type materializeResponse struct {
	TilesWritten int    `json:"tiles_written"`
	LayerID      int64  `json:"layer_id"`
	Seed         uint64 `json:"seed"`
}

func postMapMaterialize(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req materializeRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		res, err := d.Maps.MaterializeProcedural(r.Context(), mapsservice.MaterializeProceduralInput{
			MapID:   id,
			Seed:    req.Seed,
			LayerID: req.LayerID,
		})
		if err != nil {
			switch {
			case errors.Is(err, mapsservice.ErrMapNotFound):
				http.Error(w, "not found", http.StatusNotFound)
			case errors.Is(err, mapsservice.ErrNotProcedural),
				errors.Is(err, mapsservice.ErrNotPersistent),
				errors.Is(err, mapsservice.ErrNoBaseLayer),
				errors.Is(err, mapsservice.ErrNoTileKinds):
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			case errors.Is(err, wfc.ErrInvalidRegion):
				http.Error(w, "invalid map dimensions", http.StatusBadRequest)
			case errors.Is(err, wfc.ErrTooManyReseeds):
				http.Error(w, "wfc could not satisfy constraints; try a different seed", http.StatusUnprocessableEntity)
			default:
				slog.Error("wfc materialize", "err", err, "map_id", id)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(materializeResponse{
			TilesWritten: res.TilesWritten,
			LayerID:      res.LayerID,
			Seed:         res.Seed,
		})
	}
}

func deleteMap(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Maps.Delete(r.Context(), id); err != nil {
			if errors.Is(err, mapsservice.ErrMapNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("map delete", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items, _ := d.Maps.List(r.Context(), "")
		renderHTML(w, r, views.MapsGrid(views.MapsListProps{Items: items}))
	}
}

// getMapSettingsModal renders the per-map settings drawer that opens
// from the Mapmaker header. Reuses the generic Form partial driven by
// MapDraft.Descriptor() so adding a new field to the draft auto-shows
// up here. Pre-populates Values from the live row so the form is the
// "source of truth + last saved draft" surface.
func getMapSettingsModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		values := map[string]any{
			"name":             m.Name,
			"public":           m.Public,
			"instancing_mode":  m.InstancingMode,
			"persistence_mode": m.PersistenceMode,
			"spectator_policy": m.SpectatorPolicy,
		}
		if m.RefreshWindowSeconds != nil {
			values["refresh_window_seconds"] = *m.RefreshWindowSeconds
		}
		if m.Seed != nil {
			values["seed"] = *m.Seed
		}
		// If a draft exists, prefer its values so users see what they
		// last typed (not what's live). Failure here is non-fatal —
		// fall back to the live values.
		var draftJSON []byte
		row := d.Maps.Pool.QueryRow(r.Context(),
			`SELECT draft_json FROM drafts WHERE artifact_kind = 'map' AND artifact_id = $1`, id)
		if err := row.Scan(&draftJSON); err == nil && len(draftJSON) > 0 {
			var d mapsservice.MapDraft
			if jerr := json.Unmarshal(draftJSON, &d); jerr == nil {
				values["name"] = d.Name
				values["public"] = d.Public
				if d.InstancingMode != "" {
					values["instancing_mode"] = d.InstancingMode
				}
				if d.PersistenceMode != "" {
					values["persistence_mode"] = d.PersistenceMode
				}
				if d.SpectatorPolicy != "" {
					values["spectator_policy"] = d.SpectatorPolicy
				}
				if d.RefreshWindowSeconds != nil {
					values["refresh_window_seconds"] = *d.RefreshWindowSeconds
				}
				if d.Seed != nil {
					values["seed"] = *d.Seed
				}
			}
		}
		renderHTML(w, r, views.MapSettingsModal(views.MapSettingsProps{
			Map:    *m,
			Fields: mapsservice.MapDraft{}.Descriptor(),
			Values: values,
		}))
	}
}

// postMapDraft persists a MapDraft into the drafts table. Mirrors
// postEntityDraft / postAssetDraft. The publish pipeline (Push to Live)
// applies it later via the existing maps.Handler in artifact.go, which
// handles the public, instancing, persistence, spectator, and seed
// columns. Tile placements are NOT routed through here; they go to the
// dedicated /tiles endpoints and update the live row directly because
// they're high-frequency edits.
func postMapDraft(d Deps) http.HandlerFunc {
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
		if _, err := d.Maps.FindByID(r.Context(), id); err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft := mapsservice.MapDraft{
			Name:            strings.TrimSpace(r.FormValue("name")),
			Public:          parseCheckbox(r.FormValue("public")),
			InstancingMode:  strings.TrimSpace(r.FormValue("instancing_mode")),
			PersistenceMode: strings.TrimSpace(r.FormValue("persistence_mode")),
			SpectatorPolicy: strings.TrimSpace(r.FormValue("spectator_policy")),
		}
		if v := strings.TrimSpace(r.FormValue("refresh_window_seconds")); v != "" {
			if n, err := strconvAtoi64(v); err == nil {
				rw := int32(n)
				draft.RefreshWindowSeconds = &rw
			}
		}
		if v := strings.TrimSpace(r.FormValue("seed")); v != "" {
			if n, err := strconvAtoi64(v); err == nil {
				draft.Seed = &n
			}
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
		if _, err := d.Maps.Pool.Exec(r.Context(), `
			INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (artifact_kind, artifact_id) DO UPDATE
			SET draft_json = EXCLUDED.draft_json,
			    updated_at = now()
		`, "map", id, body, dr.ID); err != nil {
			slog.Error("save map draft", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeDraftSavedToast(w, "map")
	}
}

// postMapPublicToggle flips the map's `public` column directly (no
// draft pipeline) and returns the refreshed badge for HTMX outerHTML
// swap. Map visibility is the one map-level field that wants the
// painting tempo, not the draft tempo: collapsing a 5-step "draft +
// push to live" workflow into a single click matches how tile
// placements already bypass drafts.
//
// We deliberately do NOT round-trip through the publish pipeline.
// `maps.public` is a runtime metadata flag — players who hit
// /play/maps see the new value on next page load. Other map fields
// (instancing, persistence, spectator policy) still use the draft
// pipeline because they invalidate caches / live world state.
func postMapPublicToggle(d Deps) http.HandlerFunc {
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
		var nextPublic bool
		err = d.Maps.Pool.QueryRow(r.Context(),
			`UPDATE maps SET public = NOT public, updated_at = now()
			 WHERE id = $1 RETURNING public`,
			id,
		).Scan(&nextPublic)
		if err != nil {
			slog.Error("map public toggle", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Return a refreshed badge that itself acts as the toggle, so
		// the next click re-fires this endpoint. Outer-HTML swap
		// replaces the whole element so click handlers stay consistent.
		renderHTML(w, r, views.MapPublicBadge(views.MapPublicBadgeProps{
			MapID:  id,
			Public: nextPublic,
		}))
	}
}

// postMapLayerYSortToggle flips map_layers.y_sort_entities for one
// (map_id, layer_id) pair and returns the refreshed chip for HTMX
// outerHTML swap. Like postMapPublicToggle, this is a runtime metadata
// flag -- we deliberately bypass the draft pipeline so designers see
// the painting-tempo response (one click, instant feedback).
//
// Tenant safety: the UPDATE constrains by BOTH layer id AND map id, so
// a designer who guesses a layer id that isn't on the map in the URL
// gets a 404 and never mutates someone else's data. The outer auth
// middleware ensures only signed-in designers can hit the route at all.
//
// Indie-RPG research §P1 #8.
func postMapLayerYSortToggle(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dr := CurrentDesigner(r.Context()); dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mapID, err := strconvAtoi64(r.PathValue("mapID"))
		if err != nil {
			http.Error(w, "invalid map id", http.StatusBadRequest)
			return
		}
		layerID, err := strconvAtoi64(r.PathValue("layerID"))
		if err != nil {
			http.Error(w, "invalid layer id", http.StatusBadRequest)
			return
		}
		// Trust the explicit ?on= toggle target from the chip; the
		// chip always renders with the value it expects to flip TO,
		// which makes the request idempotent under double-click.
		on := r.URL.Query().Get("on") == "true"

		var refreshed mapsservice.Layer
		row := d.Maps.Pool.QueryRow(r.Context(),
			`UPDATE map_layers
			   SET y_sort_entities = $3
			 WHERE id = $1 AND map_id = $2
			 RETURNING id, map_id, name, kind, ord, y_sort_entities, created_at`,
			layerID, mapID, on,
		)
		if err := row.Scan(&refreshed.ID, &refreshed.MapID, &refreshed.Name, &refreshed.Kind, &refreshed.Ord, &refreshed.YSortEntities, &refreshed.CreatedAt); err != nil {
			// pgx returns its sentinel error here when zero rows match;
			// we treat any scan failure as 404 because the only legit
			// error path in this UPDATE...RETURNING is "row didn't exist".
			// Other failures (connection drops, etc.) are still logged.
			if strings.Contains(err.Error(), "no rows") {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("layer y-sort toggle", "err", err, "map_id", mapID, "layer_id", layerID)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.LayerYSortChip(refreshed))
	}
}

// ---- HUD editor (per-realm HUD authoring) -----------------------------
//
// Edits land directly on maps.hud_layout_json (no draft staging for v1)
// because the HUD is a viewer-only artifact: there's no live-instance
// state to hot-swap, and no half-finished layout can corrupt running
// players' state. Mirrors the map_tiles + lighting cells pattern.
//
// Tenant isolation is enforced by every Mutate / Get call passing both
// (mapID, ownerID) into the composite-key SQL. A designer can only ever
// see / edit HUDs on maps they own.

// hudLoadCommonDeps assembles the three lists every HUD page + form
// needs: ui_panel skin assets, sprite/tile icon assets, flag keys, and
// action-group names. Each is one query; no N+1.
func (d Deps) hudLoadCommonDeps(r *http.Request, mapID int64) (skins, icons []views.HUDAssetOption, flagKeys, groupNames []string, err error) {
	if d.Assets != nil {
		ui, lerr := d.Assets.List(r.Context(), assets.ListOpts{Kind: assets.KindUIPanel, Limit: 200})
		if lerr != nil {
			return nil, nil, nil, nil, lerr
		}
		for _, a := range ui {
			skins = append(skins, views.HUDAssetOption{ID: a.ID, Name: a.Name})
		}
		// Icons: sprites + tiles (icon_counter accepts both).
		sp, lerr := d.Assets.List(r.Context(), assets.ListOpts{Kind: assets.KindSprite, Limit: 200})
		if lerr != nil {
			return nil, nil, nil, nil, lerr
		}
		for _, a := range sp {
			icons = append(icons, views.HUDAssetOption{ID: a.ID, Name: a.Name})
		}
		tl, lerr := d.Assets.List(r.Context(), assets.ListOpts{Kind: assets.KindTile, Limit: 200})
		if lerr != nil {
			return nil, nil, nil, nil, lerr
		}
		for _, a := range tl {
			icons = append(icons, views.HUDAssetOption{ID: a.ID, Name: a.Name})
		}
	}
	if d.Flags != nil {
		fs, lerr := d.Flags.LoadAll(r.Context(), mapID)
		if lerr != nil {
			return nil, nil, nil, nil, lerr
		}
		for _, f := range fs {
			flagKeys = append(flagKeys, f.Key)
		}
	}
	if d.ActionGroups != nil {
		gs, lerr := d.ActionGroups.ListByMap(r.Context(), mapID)
		if lerr != nil {
			return nil, nil, nil, nil, lerr
		}
		for _, g := range gs {
			groupNames = append(groupNames, g.Name)
		}
	}
	return skins, icons, flagKeys, groupNames, nil
}

func getMapHUDPage(d Deps) http.HandlerFunc {
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
		m, err := d.Maps.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if d.HUD == nil {
			http.Error(w, "hud subsystem unavailable", http.StatusServiceUnavailable)
			return
		}
		layout, err := d.HUD.Get(r.Context(), id, dr.ID)
		if err != nil {
			if errors.Is(err, hud.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("hud get", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		skins, icons, flagKeys, groupNames, err := d.hudLoadCommonDeps(r, id)
		if err != nil {
			slog.Error("hud common deps", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		shell := BuildChrome(r, d)
		shell.Title = "HUD · " + m.Name
		shell.Surface = "hud-editor"
		shell.ActiveKind = "map"
		shell.ActiveID = m.ID
		shell.Variant = "bleed"
		renderHTML(w, r, views.HUDPage(views.HUDEditorProps{
			Layout:        shell,
			Map:           *m,
			HUD:           layout,
			WidgetKinds:   hud.AllWidgetKinds,
			FlagKeys:      flagKeys,
			ActionGroups:  groupNames,
			UIPanelAssets: skins,
			IconAssets:    icons,
		}))
	}
}

func postHUDWidgetAdd(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mapID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		anchor := hud.Anchor(r.URL.Query().Get("anchor"))
		kind := hud.WidgetKind(r.URL.Query().Get("type"))
		newLayout, err := d.HUD.Mutate(r.Context(), mapID, dr.ID, func(l *hud.Layout) error {
			_, err := l.AddWidget(anchor, kind, d.HUDWidgets)
			return err
		})
		if err != nil {
			hudWriteError(w, err)
			return
		}
		hudRenderAnchor(w, r, mapID, anchor, newLayout)
	}
}

func deleteHUDWidget(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mapID, anchor, order, err := hudPathParts(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		newLayout, err := d.HUD.Mutate(r.Context(), mapID, dr.ID, func(l *hud.Layout) error {
			l.RemoveWidget(anchor, order)
			return nil
		})
		if err != nil {
			hudWriteError(w, err)
			return
		}
		hudRenderAnchor(w, r, mapID, anchor, newLayout)
	}
}

func postHUDWidgetMove(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mapID, anchor, order, err := hudPathParts(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dir := 1
		if r.URL.Query().Get("dir") == "-1" {
			dir = -1
		}
		newLayout, err := d.HUD.Mutate(r.Context(), mapID, dr.ID, func(l *hud.Layout) error {
			return l.MoveWidget(anchor, order, dir)
		})
		if err != nil {
			hudWriteError(w, err)
			return
		}
		hudRenderAnchor(w, r, mapID, anchor, newLayout)
	}
}

func getHUDWidgetForm(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mapID, anchor, order, err := hudPathParts(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		layout, err := d.HUD.Get(r.Context(), mapID, dr.ID)
		if err != nil {
			hudWriteError(w, err)
			return
		}
		stack, ok := layout.Anchors[anchor]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var w0 *hud.Widget
		for i := range stack.Widgets {
			if stack.Widgets[i].Order == order {
				w0 = &stack.Widgets[i]
				break
			}
		}
		if w0 == nil {
			http.NotFound(w, r)
			return
		}
		cfg, err := d.HUDWidgets.New(w0.Type)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Decode current values into the form's value map. We marshal/
		// unmarshal through `any` so the form renderer's stringValue helper
		// can stringify uniformly across kinds.
		values := map[string]any{}
		if len(w0.Config) > 0 {
			_ = json.Unmarshal(w0.Config, &values)
		}
		skins, _, _, _, err := d.hudLoadCommonDeps(r, mapID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.HUDWidgetForm(views.HUDWidgetFormProps{
			MapID:         mapID,
			Anchor:        anchor,
			Order:         order,
			Kind:          w0.Type,
			Fields:        cfg.Descriptor(),
			Values:        values,
			Skin:          w0.Skin,
			Tint:          w0.Tint,
			Size:          w0.Size,
			UIPanelAssets: skins,
		}))
	}
}

func postHUDWidgetSave(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mapID, anchor, order, err := hudPathParts(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		// Pull the current widget so we know its kind (immutable from
		// the form) and have its descriptor for value coercion.
		layout, err := d.HUD.Get(r.Context(), mapID, dr.ID)
		if err != nil {
			hudWriteError(w, err)
			return
		}
		stack, ok := layout.Anchors[anchor]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var existing *hud.Widget
		for i := range stack.Widgets {
			if stack.Widgets[i].Order == order {
				existing = &stack.Widgets[i]
				break
			}
		}
		if existing == nil {
			http.NotFound(w, r)
			return
		}
		cfg, err := d.HUDWidgets.New(existing.Type)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw, err := jsonFromFormByDescriptor(cfg.Descriptor(), r.PostForm)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Envelope fields (skin, tint, size) come from the same form.
		var skinID int64
		if v := r.PostForm.Get("skin"); v != "" {
			skinID, _ = strconvAtoi64(v)
		}
		var tint uint32
		if v := r.PostForm.Get("tint"); v != "" {
			var n uint64
			if _, perr := fmt.Sscanf(v, "0x%X", &n); perr == nil {
				tint = uint32(n)
			}
		}
		size := hud.WidgetSize(r.PostForm.Get("size"))
		if !size.Valid() {
			size = ""
		}
		newLayout, err := d.HUD.Mutate(r.Context(), mapID, dr.ID, func(l *hud.Layout) error {
			return l.SaveWidgetConfig(anchor, order, existing.Type, hud.WidgetEnvelopeUpdate{
				Config: raw,
				Skin:   skinID,
				Tint:   tint,
				Size:   size,
			}, d.HUDWidgets)
		})
		if err != nil {
			hudWriteError(w, err)
			return
		}
		hudRenderAnchor(w, r, mapID, anchor, newLayout)
	}
}

func postHUDStackMetadata(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mapID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		anchor := hud.Anchor(r.PathValue("anchor"))
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		dir := hud.StackDir(r.PostForm.Get("dir"))
		gap, _ := strconvAtoi64(r.PostForm.Get("gap"))
		ox, _ := strconvAtoi64(r.PostForm.Get("offsetX"))
		oy, _ := strconvAtoi64(r.PostForm.Get("offsetY"))
		newLayout, err := d.HUD.Mutate(r.Context(), mapID, dr.ID, func(l *hud.Layout) error {
			return l.SaveStackMetadata(anchor, dir, int(gap), int(ox), int(oy))
		})
		if err != nil {
			hudWriteError(w, err)
			return
		}
		hudRenderAnchor(w, r, mapID, anchor, newLayout)
	}
}

// hudPathParts extracts (mapID, anchor, order) from a /hud/widgets/{anchor}/{order}
// route. Returns 400 on any malformed input.
func hudPathParts(r *http.Request) (int64, hud.Anchor, int, error) {
	mapID, err := pathID(r)
	if err != nil {
		return 0, "", 0, err
	}
	anchor := hud.Anchor(r.PathValue("anchor"))
	orderStr := r.PathValue("order")
	order, err := strconv.Atoi(orderStr)
	if err != nil {
		return 0, "", 0, fmt.Errorf("bad order: %w", err)
	}
	return mapID, anchor, order, nil
}

// hudRenderAnchor returns the updated anchor cell as HTML for HTMX
// outerHTML swap. Falls back to a 200 No Content when the anchor was
// emptied (the cell still renders, just with zero widgets, so the
// designer can see it's gone).
func hudRenderAnchor(w http.ResponseWriter, r *http.Request, mapID int64, anchor hud.Anchor, layout hud.Layout) {
	props := views.HUDEditorProps{
		Map: mapsservice.Map{ID: mapID},
		HUD: layout,
	}
	renderHTML(w, r, views.HUDAnchorCell(props, anchor))
}

// hudWriteError surfaces hud-package + repo errors as the appropriate
// HTTP status. ErrNotFound is 404; anything else is 400 (invalid input)
// since the only failure modes are validation errors at this point.
func hudWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, hud.ErrNotFound) {
		http.NotFound(w, nil)
		return
	}
	if errors.Is(err, hud.ErrUnknownWidget) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Warn("hud editor", "err", err)
	http.Error(w, err.Error(), http.StatusBadRequest)
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
//   - Treats the upload as a fresh asset of the same kind (new
//     content-addressed path; the original asset row is unchanged).
//   - Triggers re-bake of the new asset's palette variants (it has none
//     unless the designer copied them later — handled by a separate
//     "duplicate variants" surface).
//
// Why a NEW asset row rather than mutating the existing one?
//   - Content-addressed paths are immutable — the old row's path keeps its
//     bytes intact for any in-flight references.
//   - Designers can compare old vs new before swapping references.
//   - Avoids a destructive flow that's hard to undo.
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

// postAssetUpload accepts a multipart form upload (one OR many files),
// pushes each into object storage at a content-addressed path, creates
// the asset rows, and returns either a per-file summary (HTMX) or a
// stable JSON shape (programmatic).
//
// JSON shape:
//
//	{
//	  "results": [
//	    { "asset": {...}, "reused": bool, "original_fn": "tile_a.png" },
//	    { "error": "upload: unsupported content type", "original_fn": "weird.bmp" }
//	  ],
//	  "ok_count": 5,
//	  "err_count": 1
//	}
func postAssetUpload(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Read kind from query (programmatic callers) OR the multipart
		// form (the UI). Previously only the query was checked, so the
		// modal's <select name="kind"> was silently ignored and tile
		// uploads were classified as sprites. ParseMultipartForm is
		// idempotent, so the inner UploadMany call is unaffected.
		_ = r.ParseMultipartForm(int64(assets.MaxUploadBytes) * int64(assets.MaxFilesPerUpload))
		kindOverride := assets.NormalizeUploadKind(firstNonEmpty(
			r.URL.Query().Get("kind"),
			r.FormValue("kind"),
		))

		results, err := d.Assets.UploadMany(r.Context(), r, d.ObjectStore, dr.ID, kindOverride)
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

		ok, fail := 0, 0
		viewItems := make([]views.AssetUploadItem, 0, len(results))
		for _, res := range results {
			item := views.AssetUploadItem{OriginalFn: res.OriginalFn}
			switch {
			case res.Err != nil:
				fail++
				item.Err = res.Err.Error()
			case res.Asset != nil:
				ok++
				item.AssetID = res.Asset.ID
				item.Name = res.Asset.Name
				item.Kind = string(res.Asset.Kind)
				item.Reused = res.Reused
				// Tile uploads auto-slice into one entity_type per
				// non-empty 32x32 cell so the sheet is paintable in
				// the Mapmaker palette without a "promote" step.
				// Idempotent: cells that already have an entity
				// (re-upload, sheet sliced before this code shipped,
				// etc.) are skipped, so designers never see dupes.
				if res.Asset.Kind == assets.KindTile && len(res.TileCells) > 0 {
					n, perr := autoSliceTileSheet(r.Context(), d, res.Asset, res.TileCells, dr.ID)
					if perr != nil {
						slog.Warn("auto-slice tile sheet",
							"err", perr,
							"asset_id", res.Asset.ID,
						)
					} else {
						item.TileEntityCount = n
					}
				}
				if res.Asset.Kind == assets.KindSprite {
					summary := assets.SpriteSummaryFromImport(res.SpriteImport)
					if !summary.IsSheet() {
						summary = assets.SpriteSummaryFromMetadata(res.Asset.MetadataJSON)
					}
					item.SpriteFrameCount = summary.Frames
					item.SpriteAnimationCount = summary.Animations
				}
			}
			viewItems = append(viewItems, item)
		}

		if r.Header.Get("HX-Request") == "true" {
			renderHTML(w, r, views.AssetUploadResults(views.AssetUploadResultsProps{
				Items:    viewItems,
				OKCount:  ok,
				ErrCount: fail,
			}))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		jsonResults := make([]map[string]any, 0, len(results))
		for _, r := range results {
			entry := map[string]any{"original_fn": r.OriginalFn}
			if r.Err != nil {
				entry["error"] = r.Err.Error()
			} else {
				entry["asset"] = r.Asset
				entry["reused"] = r.Reused
				if r.Asset != nil && r.Asset.Kind == assets.KindSprite {
					summary := assets.SpriteSummaryFromImport(r.SpriteImport)
					if !summary.IsSheet() {
						summary = assets.SpriteSummaryFromMetadata(r.Asset.MetadataJSON)
					}
					if summary.IsSheet() {
						entry["sprite_sheet"] = summary
					}
				}
			}
			jsonResults = append(jsonResults, entry)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results":   jsonResults,
			"ok_count":  ok,
			"err_count": fail,
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

// ---- Push-to-Live -----------------------------------------------------

// getPublishPreview renders the diff preview modal. PLAN.md §134:
// summary lines come from publish_diffs.summary_line; structured
// diffs (per-field changes) come from configurable.StructuredDiff.
// The modal is HTMX-loaded into #modal-host from the shell.
func getPublishPreview(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.PublishPipeline == nil {
			http.Error(w, "publish pipeline not configured", http.StatusServiceUnavailable)
			return
		}
		outcomes, err := d.PublishPipeline.Preview(r.Context())
		if err != nil {
			slog.Error("publish preview", "err", err)
			http.Error(w, "preview failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		items := make([]views.PublishOutcome, 0, len(outcomes))
		for _, o := range outcomes {
			items = append(items, views.PublishOutcome{
				Kind:        string(o.Kind),
				ArtifactID:  o.ArtifactID,
				Op:          string(o.Op),
				SummaryLine: o.SummaryLine,
				Changes:     diffChangesToView(o.Diff),
			})
		}
		renderHTML(w, r, views.PublishPreviewModal(views.PublishPreviewProps{
			Items: items,
		}))
	}
}

// postPublish runs the pipeline + redirects back to the referrer (the
// design surface the user pushed from). HTMX swaps a small toast
// element into #publish-status on completion.
func postPublish(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.PublishPipeline == nil {
			http.Error(w, "publish pipeline not configured", http.StatusServiceUnavailable)
			return
		}
		dr := CurrentDesigner(r.Context())
		outcomes, err := d.PublishPipeline.Run(r.Context(), dr.ID)
		if err != nil {
			slog.Error("publish run", "designer", dr.ID, "err", err)
			http.Error(w, "publish failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.PublishResultToast(views.PublishResultProps{
			Count:       len(outcomes),
			ChangesetID: changesetIDFromOutcomes(outcomes),
		}))
	}
}

func changesetIDFromOutcomes(out []artifact.PublishOutcome) int64 {
	if len(out) == 0 {
		return 0
	}
	return out[0].ChangesetID
}

func diffChangesToView(d configurable.StructuredDiff) []views.DiffChange {
	out := make([]views.DiffChange, 0, len(d.Changes))
	for _, c := range d.Changes {
		out = append(out, views.DiffChange{Path: c.Path, Op: c.Op})
	}
	return out
}

// ---- Authored-mode mapmaker painting -------------------------------------

// tileWireFmt is the per-tile JSON the mapmaker JS reads/writes. Flat
// shape, snake_case, all numbers; matches what the canvas needs to
// render and what PlaceTiles takes server-side.
type tileWireFmt struct {
	LayerID         int64 `json:"layer_id"`
	X               int32 `json:"x"`
	Y               int32 `json:"y"`
	EntityTypeID    int64 `json:"entity_type_id"`
	RotationDegrees int16 `json:"rotation_degrees"`
}

// getMapTiles returns every tile across every layer of the map. The
// payload is a single flat array; the client groups by layer for
// rendering. v1 has no chunking — maps are bounded by Width × Height
// already and the design canvas is < 256² in practice.
func getMapTiles(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		tiles, err := d.Maps.ChunkTiles(r.Context(), id, 0, 0, m.Width-1, m.Height-1)
		if err != nil {
			slog.Error("read tiles", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out := make([]tileWireFmt, 0, len(tiles))
		for _, t := range tiles {
			out = append(out, tileWireFmt{
				LayerID: t.LayerID, X: t.X, Y: t.Y, EntityTypeID: t.EntityTypeID, RotationDegrees: t.RotationDegrees,
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"map_id": id, "width": m.Width, "height": m.Height, "tiles": out,
		})
	}
}

// postMapTiles upserts a batch of placements. Body: { tiles: [tileWireFmt, ...] }.
// Returns the count of rows written so the client can update its
// optimistic state with the authoritative number.
func postMapTiles(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body struct {
			Tiles []tileWireFmt `json:"tiles"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(body.Tiles) == 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"placed":0}`))
			return
		}
		// Bound the batch so a runaway bucket-fill can't OOM the server.
		const maxBatch = 4096
		if len(body.Tiles) > maxBatch {
			http.Error(w, fmt.Sprintf("batch too large: %d tiles (max %d)", len(body.Tiles), maxBatch), http.StatusRequestEntityTooLarge)
			return
		}
		tiles := make([]mapsservice.Tile, 0, len(body.Tiles))
		for _, t := range body.Tiles {
			if !mapsservice.ValidRotationDegrees(t.RotationDegrees) {
				http.Error(w, fmt.Sprintf("invalid rotation_degrees: %d", t.RotationDegrees), http.StatusBadRequest)
				return
			}
			tiles = append(tiles, mapsservice.Tile{
				MapID: id, LayerID: t.LayerID, X: t.X, Y: t.Y, EntityTypeID: t.EntityTypeID, RotationDegrees: t.RotationDegrees,
			})
		}
		if err := d.Maps.PlaceTiles(r.Context(), tiles); err != nil {
			slog.Error("place tiles", "err", err, "map_id", id, "n", len(tiles))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"placed": len(tiles)})
	}
}

// deleteMapTiles erases a batch of cells. Body:
// { layer_id: N, points: [[x,y], ...] }. Empty points is a no-op.
func deleteMapTiles(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body struct {
			LayerID int64      `json:"layer_id"`
			Points  [][2]int32 `json:"points"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(body.Points) == 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"erased":0}`))
			return
		}
		const maxBatch = 4096
		if len(body.Points) > maxBatch {
			http.Error(w, fmt.Sprintf("batch too large: %d points (max %d)", len(body.Points), maxBatch), http.StatusRequestEntityTooLarge)
			return
		}
		if err := d.Maps.EraseTiles(r.Context(), id, body.LayerID, body.Points); err != nil {
			slog.Error("erase tiles", "err", err, "map_id", id, "n", len(body.Points))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"erased": len(body.Points)})
	}
}

// ---- Ref pickers --------------------------------------------------------

// getPickerAssets serves the asset picker. The form's refField opens it
// with target_id / target_label query params naming the calling form's
// hidden input + visible label; tags=... narrow by asset kind. The
// HTMX search inside the modal hits this same route to swap the grid
// in place (HX-Request → return only the grid; otherwise the modal
// shell).
func getPickerAssets(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		opts := assets.ListOpts{
			Search: strings.TrimSpace(q.Get("q")),
			Limit:  100,
		}
		// Tag filter doubles as kind filter for assets: ?tags=sprite
		// means "only sprite-kind". The descriptor's RefTags lists the
		// acceptable kinds; we OR them together client-side by listing
		// each as a separate tags param.
		tags := q["tags"]
		var picked []assets.Asset
		if len(tags) == 0 {
			items, err := d.Assets.List(r.Context(), opts)
			if err != nil {
				http.Error(w, "list assets: "+err.Error(), http.StatusInternalServerError)
				return
			}
			picked = items
		} else {
			seen := map[int64]struct{}{}
			for _, t := range tags {
				kindOpts := opts
				kindOpts.Kind = assets.Kind(t)
				items, err := d.Assets.List(r.Context(), kindOpts)
				if err != nil {
					http.Error(w, "list assets: "+err.Error(), http.StatusInternalServerError)
					return
				}
				for _, a := range items {
					if _, ok := seen[a.ID]; ok {
						continue
					}
					seen[a.ID] = struct{}{}
					picked = append(picked, a)
				}
			}
		}

		props := views.PickerProps{
			Kind:      "asset",
			Title:     "Choose an asset",
			Search:    opts.Search,
			TagFilter: tags,
			TargetID:  q.Get("target_id"),
			TargetLbl: q.Get("target_label"),
			Assets:    picked,
			PublicURL: assetPublicURLFunc(picked),
		}
		if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Trigger-Name") == "q" {
			renderHTML(w, r, views.PickerGrid(props))
			return
		}
		renderHTML(w, r, views.PickerModal(props))
	}
}

// getPickerEntities serves the entity-type picker. Same shape as the
// asset picker; entity-type tags from the descriptor (RefTags) become
// the actual tag filter (entity_types.tags column).
func getPickerEntities(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		opts := entities.ListOpts{
			Search: strings.TrimSpace(q.Get("q")),
			Limit:  100,
		}
		if tags := q["tags"]; len(tags) > 0 {
			opts.Tags = tags
		}
		items, err := d.Entities.List(r.Context(), opts)
		if err != nil {
			http.Error(w, "list entities: "+err.Error(), http.StatusInternalServerError)
			return
		}
		props := views.PickerProps{
			Kind:         "entity-type",
			Title:        "Choose an entity type",
			Search:       opts.Search,
			TagFilter:    opts.Tags,
			TargetID:     q.Get("target_id"),
			TargetLbl:    q.Get("target_label"),
			Entities:     items,
			EntitySprite: buildEntitySpritePreviews(r.Context(), d, items),
		}
		if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Trigger-Name") == "q" {
			renderHTML(w, r, views.PickerGrid(props))
			return
		}
		renderHTML(w, r, views.PickerModal(props))
	}
}

// buildEntitySpritePreviews bulk-resolves the per-entity preview info
// the picker grid needs. Single batched ListByIDs against assets — no
// per-entity DB call. Entities without a sprite (or whose sprite was
// deleted) are absent from the result map; the template falls back to
// the kind pip in that case.
func buildEntitySpritePreviews(
	ctx context.Context,
	d Deps,
	ets []entities.EntityType,
) map[int64]views.EntitySpritePreview {
	if d.Assets == nil || d.ObjectStore == nil || len(ets) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(ets))
	seen := make(map[int64]struct{}, len(ets))
	for _, e := range ets {
		if e.SpriteAssetID == nil {
			continue
		}
		if _, ok := seen[*e.SpriteAssetID]; ok {
			continue
		}
		seen[*e.SpriteAssetID] = struct{}{}
		ids = append(ids, *e.SpriteAssetID)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := d.Assets.ListByIDs(ctx, ids)
	if err != nil {
		slog.Warn("entity sprite preview bulk lookup", "err", err)
		return nil
	}
	byID := make(map[int64]assets.Asset, len(rows))
	for _, a := range rows {
		byID[a.ID] = a
	}
	previewURL := assetPublicURLFunc(rows)
	out := make(map[int64]views.EntitySpritePreview, len(ets))
	for _, e := range ets {
		if e.SpriteAssetID == nil {
			continue
		}
		a, ok := byID[*e.SpriteAssetID]
		if !ok {
			continue
		}
		preview := views.EntitySpritePreview{
			URL:        previewURL(a.ContentAddressedPath),
			AtlasIndex: e.AtlasIndex,
			AtlasCols:  1,
			TileSize:   assets.TileSize,
		}
		if md, derr := assets.DecodeTileSheetMetadata(a.MetadataJSON); derr == nil && md.Cols > 0 {
			preview.AtlasCols = int32(md.Cols)
			preview.TileSize = int32(md.TileSize)
		}
		out[e.ID] = preview
	}
	return out
}

// getPickerLookup answers a small batched name lookup so refField can
// show "asset name (#5)" instead of "currently #5" on first render.
//
// Querystring shape:
//
//	?asset=1,2,3&entity=10,11
//
// Response:
//
//	{ "asset": {"1":"Goblin","2":"Tile A"}, "entity": {"10":"Tree"} }
//
// Missing ids simply omit themselves from the response so the JS
// fallback ("currently #N") still applies. Bound to ~64 ids per kind
// so a runaway form can't stall the page.
func getPickerLookup(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const maxIDs = 64

		parseIDs := func(raw string) []int64 {
			if raw == "" {
				return nil
			}
			parts := strings.Split(raw, ",")
			if len(parts) > maxIDs {
				parts = parts[:maxIDs]
			}
			out := make([]int64, 0, len(parts))
			seen := map[int64]struct{}{}
			for _, p := range parts {
				n, err := strconvAtoi64(strings.TrimSpace(p))
				if err != nil || n <= 0 {
					continue
				}
				if _, dup := seen[n]; dup {
					continue
				}
				seen[n] = struct{}{}
				out = append(out, n)
			}
			return out
		}

		assetIDs := parseIDs(r.URL.Query().Get("asset"))
		entityIDs := parseIDs(r.URL.Query().Get("entity"))

		assetNames := make(map[string]string, len(assetIDs))
		for _, id := range assetIDs {
			if a, err := d.Assets.FindByID(r.Context(), id); err == nil {
				assetNames[fmt.Sprintf("%d", id)] = a.Name
			}
		}
		entityNames := make(map[string]string, len(entityIDs))
		for _, id := range entityIDs {
			if e, err := d.Entities.FindByID(r.Context(), id); err == nil {
				entityNames[fmt.Sprintf("%d", id)] = e.Name
			}
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"asset":  assetNames,
			"entity": entityNames,
		})
	}
}
