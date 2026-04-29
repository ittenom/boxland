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
	"boxland/server/internal/characters"
	"boxland/server/internal/configurable"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/flags"
	"boxland/server/internal/folders"
	"boxland/server/internal/hud"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/maps/wfc"
	"boxland/server/internal/persistence"
	"boxland/server/internal/publishing/artifact"
	"boxland/server/internal/settings"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/updater"
	"boxland/server/internal/worlds"
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
	Folders         *folders.Service
	Tilemaps        *tilemaps.Service
	Maps            *mapsservice.Service
	Levels          *levels.Service
	Worlds          *worlds.Service
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

	// Character generator domain. Owns slot/part/recipe/bake/stat-set
	// /talent-tree/NPC-template CRUD plus the publish artifact
	// handlers. See server/internal/characters.
	Characters *characters.Service

	// Updates is the on-disk-cached GitHub Releases probe used to
	// surface "new Boxland available" in the chrome bar and via the
	// /design/api/version JSON endpoint. nil disables both UIs and
	// is fine in tests / minimal embeddings.
	Updates *updater.Client
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

	// Update notification API. Designer-only because the response
	// hints the operator at a `boxland update` workflow that only
	// makes sense for the person who launched the server. Read-only
	// (purely a cache-read), so safe to hit on every page load.
	mux.Handle("GET /design/api/version", auth(getVersionStatus(d)))

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

	// Asset export / import (PLAN.md backbone for save/restore + asset
	// sharing). Each export round-trips into the matching import. See
	// internal/exporter + internal/importer.
	mux.Handle("GET /design/assets/export", auth(getAllAssetsExport(d)))
	mux.Handle("GET /design/assets/export/{id}", auth(getAssetExport(d)))
	mux.Handle("GET /design/assets/import", auth(getAssetsImportModal(d)))
	mux.Handle("POST /design/assets/import", auth(postAssetsImport(d)))

	// Folder filesystem (PLAN: asset folder system). Tree CRUD plus
	// the per-folder contents listing that powers both the Asset
	// Manager grid and the Mapmaker palette.
	mux.Handle("GET /design/folders", auth(getFolderTree(d)))
	mux.Handle("GET /design/folders/contents", auth(getFolderContents(d)))
	mux.Handle("GET /design/folders/new", auth(getFolderNewModal(d)))
	mux.Handle("POST /design/folders", auth(postFolderCreate(d)))
	mux.Handle("POST /design/folders/{id}/rename", auth(postFolderRename(d)))
	mux.Handle("POST /design/folders/{id}/move", auth(postFolderMove(d)))
	mux.Handle("POST /design/folders/{id}/sort-mode", auth(postFolderSortMode(d)))
	mux.Handle("DELETE /design/folders/{id}", auth(deleteFolder(d)))
	mux.Handle("POST /design/assets/move", auth(postAssetsMove(d)))
	mux.Handle("POST /design/assets/{id}/rename", auth(postAssetRename(d)))

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

	// Character generator (Phase 1: dashboard + slot/part/NPC-template
	// CRUD + draft endpoints. Generator UI in Phase 2.) See
	// docs/superpowers/plans/2026-04-26-character-generator-plan.md.
	mux.Handle("GET /design/characters", auth(getCharactersList(d)))
	mux.Handle("POST /design/characters/slots", auth(postCharacterSlot(d)))
	mux.Handle("DELETE /design/characters/slots/{id}", auth(deleteCharacterSlot(d)))
	mux.Handle("POST /design/characters/slots/{id}/draft", auth(postCharacterSlotDraft(d)))
	mux.Handle("POST /design/characters/parts", auth(postCharacterPart(d)))
	mux.Handle("DELETE /design/characters/parts/{id}", auth(deleteCharacterPart(d)))
	mux.Handle("POST /design/characters/parts/{id}/draft", auth(postCharacterPartDraft(d)))
	mux.Handle("GET /design/characters/npc-templates/new", auth(getCharacterNpcTemplateNewModal(d)))
	mux.Handle("POST /design/characters/npc-templates", auth(postCharacterNpcTemplate(d)))
	mux.Handle("DELETE /design/characters/npc-templates/{id}", auth(deleteCharacterNpcTemplate(d)))
	mux.Handle("POST /design/characters/npc-templates/{id}/draft", auth(postCharacterNpcTemplateDraft(d)))
	mux.Handle("POST /design/characters/npc-templates/{id}/attach-recipe", auth(postCharacterNpcTemplateAttachRecipe(d)))
	mux.Handle("GET /design/characters/generator/{id}", auth(getCharacterGeneratorPage(d)))
	// Recipe + catalog endpoints for the Generator UI (Phase 2).
	mux.Handle("GET /design/characters/catalog", auth(getCharacterCatalog(d)))
	mux.Handle("POST /design/characters/recipes", auth(postCharacterRecipe(d)))
	mux.Handle("GET /design/characters/recipes/{id}", auth(getCharacterRecipe(d)))
	mux.Handle("POST /design/characters/recipes/{id}", auth(updateCharacterRecipe(d)))

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

	mux.Handle("GET /design/maps/{id}/locks", auth(getMapLocks(d)))
	mux.Handle("POST /design/maps/{id}/locks", auth(postMapLocks(d)))
	mux.Handle("DELETE /design/maps/{id}/locks", auth(deleteMapLocks(d)))
	mux.Handle("GET /design/maps/{id}/sample-patch", auth(getMapSamplePatch(d)))
	mux.Handle("POST /design/maps/{id}/sample-patch", auth(postMapSamplePatch(d)))
	mux.Handle("DELETE /design/maps/{id}/sample-patch", auth(deleteMapSamplePatch(d)))
	mux.Handle("POST /design/entity-types/{id}/procedural-include", auth(postEntityTypeProceduralInclude(d)))
	mux.Handle("GET /design/maps/{id}/constraints", auth(getMapConstraints(d)))
	mux.Handle("POST /design/maps/{id}/constraints", auth(postMapConstraint(d)))
	mux.Handle("DELETE /design/maps/{id}/constraints/{cid}", auth(deleteMapConstraint(d)))
	mux.Handle("DELETE /design/maps/{id}", auth(deleteMap(d)))
	mux.Handle("GET /design/maps/{id}/settings", auth(getMapSettingsModal(d)))
	mux.Handle("POST /design/maps/{id}/draft", auth(postMapDraft(d)))
	// "Public" moved to LEVEL in the holistic redesign; the level
	// editor's settings tab owns the toggle now (see
	// /design/levels/{id}/public-toggle).
	mux.Handle("POST /design/maps/{mapID}/layers/{layerID}/y-sort", auth(postMapLayerYSortToggle(d)))

	// Map export / import (mirrors the asset surface — see PLAN.md
	// backbone notes in internal/exporter).
	mux.Handle("GET /design/maps/{id}/export", auth(getMapExport(d)))
	mux.Handle("POST /design/maps/import", auth(postMapImport(d)))

	// Per-level HUD editor. Edits land on levels.hud_layout_json
	// directly (no draft staging for v1 — same pattern as map_tiles
	// and lighting cells). Per the holistic redesign, HUD lives on
	// LEVELs, not maps.
	mux.Handle("GET /design/levels/{id}/hud", auth(getMapHUDPage(d)))
	mux.Handle("POST /design/levels/{id}/hud/widgets", auth(postHUDWidgetAdd(d)))
	mux.Handle("GET /design/levels/{id}/hud/widgets/{anchor}/{order}", auth(getHUDWidgetForm(d)))
	mux.Handle("POST /design/levels/{id}/hud/widgets/{anchor}/{order}", auth(postHUDWidgetSave(d)))
	mux.Handle("DELETE /design/levels/{id}/hud/widgets/{anchor}/{order}", auth(deleteHUDWidget(d)))
	mux.Handle("POST /design/levels/{id}/hud/widgets/{anchor}/{order}/move", auth(postHUDWidgetMove(d)))
	mux.Handle("POST /design/levels/{id}/hud/anchors/{anchor}", auth(postHUDStackMetadata(d)))

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

	// Sandbox. Per the holistic redesign, sandboxes launch a LEVEL
	// (geometry comes from the level's map; entity placements come
	// from level_entities). Designer realm uses the
	// `sandbox:<designer_id>:<level_id>` AOI namespace.
	mux.Handle("GET /design/sandbox", auth(getSandboxIndex(d)))
	mux.Handle("GET /design/sandbox/launch/{id}", auth(getSandboxLaunch(d)))

	// ---- Phase 2 redesign surfaces ----
	//
	// New top-level pages added by the holistic redesign:
	//   /design/worlds   — graph of LEVELs (campaign packaging)
	//   /design/levels   — MAP + entity placements + HUD + instancing
	//   /design/tilemaps — sliced tile sheets with adjacency
	//   /design/library  — Sprites/Tilemaps/Audio/UI panels tab shell
	//
	// Plus per-class entity pages
	// (/design/entities/{tiles|npcs|pcs|logic}) so designers can scope
	// their grid to one class without per-tag filters.

	mux.Handle("GET /design/worlds", auth(getWorldsList(d)))
	mux.Handle("GET /design/worlds/new", auth(getWorldNewModal(d)))
	mux.Handle("POST /design/worlds", auth(postWorldCreate(d)))
	mux.Handle("GET /design/worlds/{id}", auth(getWorldDetail(d)))
	mux.Handle("DELETE /design/worlds/{id}", auth(deleteWorld(d)))
	mux.Handle("POST /design/worlds/{id}/rename", auth(postWorldRename(d)))
	mux.Handle("POST /design/worlds/{id}/start-level", auth(postWorldStartLevel(d)))
	// World export/import — full campaign bundle.
	mux.Handle("GET /design/worlds/{id}/export", auth(getWorldExport(d)))
	mux.Handle("GET /design/worlds/import", auth(getWorldImportModal(d)))
	mux.Handle("POST /design/worlds/import", auth(postWorldImport(d)))

	mux.Handle("GET /design/levels", auth(getLevelsList(d)))
	mux.Handle("GET /design/levels/new", auth(getLevelNewModal(d)))
	mux.Handle("POST /design/levels", auth(postLevelCreate(d)))
	mux.Handle("GET /design/levels/{id}", auth(getLevelDetail(d)))
	mux.Handle("DELETE /design/levels/{id}", auth(deleteLevel(d)))
	mux.Handle("POST /design/levels/{id}/public-toggle", auth(postLevelPublic(d)))
	mux.Handle("POST /design/levels/{id}/settings", auth(postLevelSettings(d)))
	// Per-level entity placement editor: list / place / move / remove.
	// Tile placements live on the MAP (mapmaker); these are NPCs,
	// PCs, doors, region triggers, spawn points — anything with
	// coordinates that isn't tile geometry.
	mux.Handle("GET /design/levels/{id}/entities", auth(getLevelEntities(d)))
	mux.Handle("POST /design/levels/{id}/entities", auth(postLevelEntity(d)))
	mux.Handle("PATCH /design/levels/{id}/entities/{eid}", auth(patchLevelEntity(d)))
	mux.Handle("DELETE /design/levels/{id}/entities/{eid}", auth(deleteLevelEntity(d)))
	// Atlas catalog endpoints — feed the Pixi-driven editor canvases'
	// StaticAssetCatalog. tile-types is scoped to the entity_types
	// referenced by this map's geometry; entity-types is the project-
	// wide placeable (npc/pc/logic) catalog.
	mux.Handle("GET /design/maps/{id}/tile-types", auth(getMapTileTypes(d)))
	mux.Handle("GET /design/levels/{id}/entity-types", auth(getLevelEntityTypes(d)))
	// Level export/import — full bundle (level + map + entities + HUD).
	mux.Handle("GET /design/levels/{id}/export", auth(getLevelExport(d)))
	mux.Handle("GET /design/levels/import", auth(getLevelImportModal(d)))
	mux.Handle("POST /design/levels/import", auth(postLevelImport(d)))

	mux.Handle("GET /design/tilemaps", auth(getTilemapsList(d)))
	mux.Handle("GET /design/tilemaps/{id}", auth(getTilemapDetail(d)))
	mux.Handle("DELETE /design/tilemaps/{id}", auth(deleteTilemap(d)))
	// Tilemap export/import — the .boxtilemap.zip carries the row +
	// per-cell tile entities + backing PNG, round-tripping cleanly
	// across projects. URL shape mirrors the level/world side
	// (/design/<kind>s/{id}/export); the older
	// /design/assets/export/{id} route is kept for back-compat.
	mux.Handle("GET /design/tilemaps/{id}/export", auth(getTilemapExport(d)))
	mux.Handle("GET /design/tilemaps/import", auth(getTilemapImportModal(d)))
	mux.Handle("POST /design/tilemaps/import", auth(postTilemapImport(d)))

	mux.Handle("GET /design/library", auth(getLibraryPage(d)))
	mux.Handle("GET /design/library/sprites", auth(getLibraryTab("sprites")(d)))
	mux.Handle("GET /design/library/tilemaps", auth(getLibraryTab("tilemaps")(d)))
	mux.Handle("GET /design/library/audio", auth(getLibraryTab("audio")(d)))
	mux.Handle("GET /design/library/ui-panels", auth(getLibraryTab("ui-panels")(d)))

	// Per-class entity pages share the existing list handler with a
	// synthetic class= query param.
	mux.Handle("GET /design/entities/tiles", auth(getEntitiesByClass("tile")(d)))
	mux.Handle("GET /design/entities/npcs", auth(getEntitiesByClass("npc")(d)))
	mux.Handle("GET /design/entities/pcs", auth(getEntitiesByClass("pc")(d)))
	mux.Handle("GET /design/entities/logic", auth(getEntitiesByClass("logic")(d)))
	// UI-class entities: the meta loop. The same rows back the
	// editor's chrome (NineSlice button frames, panel backgrounds,
	// slider tracks) AND any in-game HUD widgets a designer skins.
	mux.Handle("GET /design/entities/ui", auth(getEntitiesByClass("ui")(d)))

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

// getVersionStatus returns the cached update status as JSON. We
// intentionally read from the on-disk cache only (Cached(), not
// CheckLatest) so a flood of designer page loads can never multiply
// into a flood of GitHub API calls — the TLI is the one place that
// refreshes the cache. The shape matches updater.Status so the
// frontend can use it without a server-side mapping layer.
func getVersionStatus(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		var s *updater.Status
		if d.Updates != nil {
			s = d.Updates.Cached()
		}
		if s == nil {
			// Always emit at least the running version so the
			// client can render "you're on vX.Y.Z" without a
			// follow-up request.
			s = &updater.Status{}
		}
		_ = json.NewEncoder(w).Encode(s)
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
		// Sum warnings across the four entity-class sections (each
		// already collected an N <= treeItemsPerSubSection sample).
		props.EntitiesNoSpr =
			countEntityWarns(layout.Tree.TileEntities) +
				countEntityWarns(layout.Tree.NPCEntities) +
				countEntityWarns(layout.Tree.PCEntities) +
				countEntityWarns(layout.Tree.LogicEntities)
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
// getSandboxIndex lists levels (per the holistic redesign — sandboxes
// launch a LEVEL, not a raw MAP). Each level card shows its backing
// map for context.
func getSandboxIndex(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// We render the existing SandboxIndex view, but feed it a
		// "maps" list that's actually levels' backing maps for now.
		// Phase 2 follow-up: a dedicated SandboxLevelIndex view that
		// makes the level/map pairing explicit and surfaces
		// non-public levels with a "private" badge.
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

// getSandboxLaunch builds a level-scoped sandbox session. The path id
// is a LEVEL id; the level resolves to a map (for geometry + WS
// JoinMap, which still keys off map_id) and an instance id of the form
// "sandbox:<designer_id>:<level_id>".
//
// The AOI subscription manager refuses player-realm subscribers to
// that id space, so the sandbox stays designer-private. If the path
// id matches a map but no level wraps it yet, we fall back to a
// map-scoped sandbox so existing /design/sandbox/launch/<map_id>
// links from the maps list still work — Phase 2 follow-up reshapes
// that picker.
func getSandboxLaunch(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}

		// First try the id as a level id.
		if d.Levels != nil {
			if lv, err := d.Levels.FindByID(r.Context(), id); err == nil && lv != nil {
				m, mErr := d.Maps.FindByID(r.Context(), lv.MapID)
				if mErr != nil || m == nil {
					http.Error(w, "level's map not found", http.StatusInternalServerError)
					return
				}
				ip := clientIP(r)
				ticket, err := d.Auth.MintWSTicket(r.Context(), dr.ID, ip)
				if err != nil {
					http.Error(w, "mint ticket: "+err.Error(), http.StatusInternalServerError)
					return
				}
				instanceID := fmt.Sprintf("sandbox:%d:%d", dr.ID, lv.ID)
				renderHTML(w, r, views.SandboxGamePage(views.SandboxGameProps{
					DesignerName: dr.Email,
					Map:          *m,
					WSURL:        resolveSandboxWSURL(r),
					WSTicket:     ticket,
					InstanceID:   instanceID,
				}))
				return
			}
		}

		// Fallback: id is a map id. Used by the existing maps-list
		// "Open in sandbox" link until that picker is reshaped.
		m, err := d.Maps.FindByID(r.Context(), id)
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
		// Map-scoped sandbox: keep the historical instance namespace
		// so the AOI manager's "sandbox:" prefix check still applies.
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
//
// The page is a two-pane IDE: folder rail on the left, contents grid
// on the right. Query parameters drive the right-pane content:
//
//   ?folder_id=<id>  → that folder's contents (uses folder.sort_mode)
//   ?kind=<kind>     → kind-root view (every asset of `kind` whose
//                      folder_id IS NULL)
//   neither set      → legacy flat grid (kept for filter=orphan deep
//                      links and other non-folder views)
//
// In every case the rail (Tree) is populated for all four kind_roots.
func getAssetsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := assetListOptsFromQuery(r)
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		// Folder / kind-root view selectors.
		var folderID *int64
		if s := strings.TrimSpace(r.URL.Query().Get("folder_id")); s != "" {
			id, err := strconv.ParseInt(s, 10, 64)
			if err == nil && id > 0 {
				folderID = &id
			}
		}
		kindRoot := strings.TrimSpace(r.URL.Query().Get("kind"))
		sort := strings.TrimSpace(r.URL.Query().Get("sort"))

		// Phase 2: kind_roots `tilemap`, `level`, `world` belong to
		// dedicated pages — redirect there so the rail link goes to
		// the right home rather than rendering an empty asset grid.
		// folder_id is preserved so a click on a tilemap-folder ROW
		// still lands on the tilemaps list (Phase 3 follow-up may
		// surface folder-scoped tilemap views).
		switch kindRoot {
		case "tilemap":
			http.Redirect(w, r, "/design/tilemaps", http.StatusFound)
			return
		case "level":
			http.Redirect(w, r, "/design/levels", http.StatusFound)
			return
		case "world":
			http.Redirect(w, r, "/design/worlds", http.StatusFound)
			return
		}

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

		// Folder rail props (always populated, all four kind_roots).
		treeProps, err := buildFolderTreeProps(r.Context(), d, "", nil)
		if err != nil {
			slog.Warn("folder tree props", "err", err)
		}
		treeProps.SelectedFolderID = folderID

		// Right-pane: folder contents view if folder_id or kind set.
		var contents *views.FolderContentsProps
		if folderID != nil || kindRoot != "" {
			if sort == "" && folderID != nil {
				if f, err := d.Folders.FindByID(r.Context(), *folderID); err == nil {
					sort = string(f.SortMode)
				}
			}
			if sort == "" {
				sort = "alpha"
			}
			folderItems, err := d.Assets.ListByFolder(r.Context(), folderID, kindRoot, sort)
			if err != nil {
				slog.Warn("list by folder", "err", err)
			}
			contents = &views.FolderContentsProps{
				Items:     folderItems,
				Sort:      sort,
				FolderID:  folderID,
				Kind:      kindRoot,
				PublicURL: assetPublicURLFunc(folderItems),
			}
		}

		// Compute the asset → entity-count map so orphan/used-by badges
		// render with real numbers. Failure degrades gracefully (badges
		// fall back to "—").
		usage, err := AssetUsageMap(r.Context(), d, assetIDs(items))
		if err != nil {
			slog.Warn("asset usage map", "err", err)
		}
		if contents != nil {
			contents.UsageByID = usage
		}

		items = applyAssetFilter(items, filter, usage)

		renderHTML(w, r, views.AssetsList(views.AssetsListProps{
			Layout:       layout,
			Items:        items,
			ActiveKind:   string(opts.Kind),
			Search:       opts.Search,
			PublicURL:    assetPublicURLFunc(items),
			Tree:         treeProps,
			Contents:     contents,
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
		modal := views.AssetDetail(views.AssetDetailProps{
			Asset:     *a,
			PublicURL: assetPublicURLFunc([]assets.Asset{*a}),
			UsedBy:    usedBy,
		})

		// HTMX swap into #modal-host — fragment-only response.
		if isHTMXRequest(r) {
			renderHTML(w, r, modal)
			return
		}

		// Full-page navigation: render the Asset Manager list with the
		// detail modal pre-injected, so deep links / bookmarks / new
		// tabs land on a fully styled page instead of a bare fragment.
		items, err := d.Assets.List(r.Context(), assets.ListOpts{})
		if err != nil {
			slog.Error("assets list (detail full-page)", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		usage, err := AssetUsageMap(r.Context(), d, assetIDs(items))
		if err != nil {
			slog.Warn("asset usage map (detail full-page)", "err", err)
		}
		layout := BuildChrome(r, d)
		layout.Title = a.Name
		layout.Surface = "asset-manager"
		layout.ActiveKind = "asset"
		layout.ActiveID = a.ID
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{
			{Label: "Assets", Href: "/design/assets"},
			{Label: a.Name},
		}
		renderHTML(w, r, views.AssetsList(views.AssetsListProps{
			Layout:    layout,
			Items:     items,
			PublicURL: assetPublicURLFunc(items),
			UsageByID: usage,
			ModalSlot: modal,
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
		// Per the holistic redesign, tile entities are minted by the
		// tilemap service from a tilemap row; this generic
		// "promote-to-entity" path produces a logic-class entity_type
		// regardless of the source asset kind. Tilemap-specific
		// fan-out happens elsewhere.
		et, err := d.Entities.Create(r.Context(), entities.CreateInput{
			Name:          newName,
			SpriteAssetID: &assetID,
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
// Used for SPRITE assets (single-cell). Tilemap-eligible uploads (a
// `sprite_animated` asset whose PNG slices into a 32×32 grid) go
// through `autoCreateTilemap` so each non-empty cell gets its own
// tile-class entity_type with the right atlas_index — the only way
// the Mapmaker palette can render real tile artwork instead of a
// yellow #1213 chip.
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

// autoCreateTilemap turns a tilemap-eligible upload into a `tilemaps`
// row + per-cell tile-class entity_types + tilemap_tiles rows. Wraps
// `tilemaps.Service.Create` with idempotency: if the asset already
// powers a tilemap (re-upload of identical bytes), the existing row
// is reused and the cell count returned reflects the existing layout.
//
// Returns the number of non-empty cells the tilemap holds. Errors per
// cell are surfaced by the underlying service; a hard failure here
// (bad PNG, etc.) is logged by the caller — the asset row still
// exists, so the upload result still says "added," and the designer
// can re-create the tilemap from the asset detail later.
//
// Naming: the tilemap takes the asset's name verbatim. The per-cell
// tile entity names use "<asset name> #r{R}c{C}" — designers find
// specific tiles by their source-sheet coordinates.
func autoCreateTilemap(
	ctx context.Context,
	d Deps,
	res assets.MultiUploadResult,
	designerID int64,
) (int, error) {
	if d.Tilemaps == nil {
		return 0, errors.New("tilemaps service not configured")
	}
	if res.Asset == nil {
		return 0, errors.New("upload result has no asset")
	}
	// Idempotency: a previous upload of the same bytes may have
	// already produced a tilemap. The asset is dedup'd by content
	// path; the tilemap-by-asset-id lookup tells us whether the
	// downstream rows survived too.
	if existing, err := d.Tilemaps.FindByAssetID(ctx, res.Asset.ID); err == nil && existing != nil {
		return int(existing.NonEmptyCount), nil
	}
	if len(res.PngBody) == 0 {
		// Re-uploads of an identical asset hit the dedup branch in
		// uploadFromHeader, which still returns the slice metadata
		// but skips the body copy. Without bytes we can't compute
		// pixel + edge hashes, so we can't create the tilemap row
		// here. The asset is still saved; the designer can create
		// the tilemap manually from /design/tilemaps if they really
		// need to (rare path; typically the original upload created
		// the tilemap and Reused=true means we already have one).
		return 0, errors.New("tilemap-eligible upload missing PNG bytes (already-imported asset)")
	}
	tm, err := d.Tilemaps.Create(ctx, tilemaps.CreateInput{
		Name:      res.Asset.Name,
		AssetID:   res.Asset.ID,
		CreatedBy: designerID,
		Cells:     res.TilemapCells,
		Meta:      res.TilemapMeta,
		PngBody:   res.PngBody,
	})
	if err != nil {
		return 0, fmt.Errorf("tilemap create: %w", err)
	}
	return int(tm.NonEmptyCount), nil
}

// autoCreateUIEntity matches each `ui_panel` asset upload with a
// matching ClassUI entity_type and a default `nine_slice` component.
// This is the meta dogfood loop: the same entity_type the editor
// renders as a button frame is the one a designer can drop on a HUD
// anchor in their game.
//
// Idempotency: a previous upload of the same bytes is dedup'd at the
// asset layer (content_addressed_path conflict yields Reused=true);
// here we additionally guard against the case where the asset row
// already exists but the entity_type was deleted manually — in that
// case we recreate the entity_type with the same name. ErrNameInUse
// from the entity_types table means an entity of the same name
// already exists with the same asset id, which is the dedup we want.
func autoCreateUIEntity(
	ctx context.Context,
	d Deps,
	asset *assets.Asset,
	designerID int64,
) error {
	if d.Entities == nil {
		return errors.New("entities service not configured")
	}
	if asset == nil {
		return errors.New("nil asset")
	}
	// Look for an existing ClassUI entity_type already pointing at
	// this asset. FindBySpriteAtlas returns every entity_type with
	// the asset id (multiple atlas cells per sheet are allowed for
	// tile sheets); we just need to find one ClassUI row.
	if existing, err := d.Entities.FindBySpriteAtlas(ctx, asset.ID); err == nil {
		for _, e := range existing {
			if e.EntityClass == entities.ClassUI {
				return nil
			}
		}
	}

	assetID := asset.ID
	et, err := d.Entities.Create(ctx, entities.CreateInput{
		Name:          asset.Name,
		EntityClass:   entities.ClassUI,
		SpriteAssetID: &assetID,
		AtlasIndex:    0,
		Tags:          []string{"ui-pack"},
		CreatedBy:     designerID,
	})
	if err != nil {
		// Name conflict means someone (or a prior auto-create) made
		// an entity with this name already. Treat as success: the
		// designer can remap it manually if they meant something
		// else. Surfacing the error here would block the upload.
		if errors.Is(err, entities.ErrNameInUse) {
			return nil
		}
		return fmt.Errorf("create ui entity_type: %w", err)
	}

	// Default nine_slice insets. The Phase 2 seeder uses a measurer
	// for the bulk pack import; per-upload UI sprites get sane
	// defaults that the designer can fine-tune in the entity
	// inspector.
	cfg, err := json.Marshal(components.NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 6})
	if err != nil {
		return fmt.Errorf("marshal default nine_slice: %w", err)
	}
	if err := d.Entities.SetComponents(ctx, nil, et.ID, map[components.Kind]json.RawMessage{
		components.KindNineSlice: cfg,
	}); err != nil {
		return fmt.Errorf("set nine_slice component: %w", err)
	}
	return nil
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
	case "character_slot":
		return `Slot draft saved.`
	case "character_part":
		return `Part draft saved.`
	case "npc_template":
		return `NPC template draft saved.`
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
	case "character_slot", "character_part":
		return `to make this change visible to the character generator.`
	case "npc_template":
		return `to bake the sprite and link the NPC entity type.`
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

// getEntitiesList renders the entities grid. Honors:
//
//   ?class=tile|npc|pc|logic   — scopes by entity_class. Per the
//                                holistic redesign, each class has its
//                                own /design/entities/{tiles|npcs|...}
//                                URL that sets this param.
//   ?class=library_sprites|library_audio|library_ui — Phase 2 stubs:
//                                redirect to the matching Library tab,
//                                which in v1 still uses the asset list.
//   ?filter=no-sprite          — narrow to entities missing a sprite.
//   ?q=...                     — search by name (ILIKE).
//   ?tags=foo,bar              — tag filter.
func getEntitiesList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := strings.TrimSpace(r.URL.Query().Get("class"))
		opts := entityListOptsFromQuery(r)
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := listEntitiesScoped(r.Context(), d, class, opts)
		if err != nil {
			slog.Error("entities list", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items = applyEntityFilter(items, filter)
		layout := BuildChrome(r, d)
		layout.Title = entitiesPageTitle(class)
		layout.Surface = "entity-manager"
		layout.ActiveKind = entityActiveKind(class)
		layout.Variant = "no-rail"
		layout.Crumbs = entitiesPageCrumbs(class)
		renderHTML(w, r, views.EntitiesList(views.EntitiesListProps{
			Layout:       layout,
			Items:        items,
			Search:       opts.Search,
			ActiveFilter: filter,
			ActiveClass:  class,
		}))
	}
}

func getEntitiesGrid(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := strings.TrimSpace(r.URL.Query().Get("class"))
		opts := entityListOptsFromQuery(r)
		filter := strings.TrimSpace(r.URL.Query().Get("filter"))
		items, err := listEntitiesScoped(r.Context(), d, class, opts)
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
			ActiveClass:  class,
		}))
	}
}

// listEntitiesScoped narrows the entity list to one class when the
// `class` query param is one of the four canonical values; otherwise
// returns the full list.
func listEntitiesScoped(ctx context.Context, d Deps, class string, opts entities.ListOpts) ([]entities.EntityType, error) {
	switch class {
	case "tile":
		return d.Entities.ListByClass(ctx, entities.ClassTile, opts)
	case "npc":
		return d.Entities.ListByClass(ctx, entities.ClassNPC, opts)
	case "pc":
		return d.Entities.ListByClass(ctx, entities.ClassPC, opts)
	case "logic":
		return d.Entities.ListByClass(ctx, entities.ClassLogic, opts)
	case "ui":
		return d.Entities.ListByClass(ctx, entities.ClassUI, opts)
	}
	return d.Entities.List(ctx, opts)
}

// entitiesPageTitle picks the page title to match the active class.
func entitiesPageTitle(class string) string {
	switch class {
	case "tile":
		return "Tiles"
	case "npc":
		return "NPCs"
	case "pc":
		return "Player characters"
	case "logic":
		return "Logic"
	case "ui":
		return "UI"
	}
	return "Entities"
}

// entityActiveKind picks the tree-highlight kind to match the active
// class (so the right sub-section gets aria-current).
func entityActiveKind(class string) string {
	switch class {
	case "tile", "npc", "pc", "logic", "ui":
		return class
	}
	return "entity"
}

// entitiesPageCrumbs builds the breadcrumb for the per-class page.
func entitiesPageCrumbs(class string) []views.Crumb {
	if class == "" {
		return []views.Crumb{{Label: "Entities"}}
	}
	return []views.Crumb{
		{Label: "Entities", Href: "/design/entities"},
		{Label: entitiesPageTitle(class)},
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

		spriteURL, atlasCols, tileSize := spriteRenderInfoFor(d, et)
		props := views.EntityDetailProps{
			EntityType:      *et,
			Components:      comps,
			AllKinds:        d.Components.Kinds(),
			Descriptors:     collectDescriptors(d.Components),
			SpriteURL:       spriteURL,
			SpriteAtlasCols: atlasCols,
			SpriteTileSize:  tileSize,
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

		modal := views.EntityDetail(props)

		// HTMX swap target (#modal-host) — return just the modal markup.
		// The list page is already on screen; styles + scripts already loaded.
		if isHTMXRequest(r) {
			renderHTML(w, r, modal)
			return
		}

		// Full-page navigation (new tab, bookmark, refresh, or HX-Redirect
		// landing from postEntityCreate). Render the styled list page with
		// the detail modal seeded into #modal-host so the user sees the
		// real Entity Manager — not an unstyled fragment.
		items, err := d.Entities.List(r.Context(), entities.ListOpts{})
		if err != nil {
			slog.Error("entities list (detail full-page)", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		layout := BuildChrome(r, d)
		layout.Title = et.Name
		layout.Surface = "entity-manager"
		layout.ActiveKind = "entity"
		layout.ActiveID = et.ID
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{
			{Label: "Entities", Href: "/design/entities"},
			{Label: et.Name},
		}
		renderHTML(w, r, views.EntitiesList(views.EntitiesListProps{
			Layout:    layout,
			Items:     items,
			ModalSlot: modal,
		}))
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
		spriteURL, atlasCols, tileSize := spriteRenderInfoFor(d, et)
		renderHTML(w, r, views.EntityDetail(views.EntityDetailProps{
			EntityType:      *et,
			Components:      comps,
			AllKinds:        d.Components.Kinds(),
			Descriptors:     collectDescriptors(d.Components),
			SpriteURL:       spriteURL,
			SpriteAtlasCols: atlasCols,
			SpriteTileSize:  tileSize,
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
	url, _, _ := spriteRenderInfoFor(d, et)
	return url
}

// spriteRenderInfoFor returns everything the entity-detail preview canvas
// needs to draw the entity's single atlas cell (not the whole sheet):
//
//   - url       public CDN URL for the source PNG, "" when unset.
//   - cols      atlas columns; 1 for single-frame sprites.
//   - tileSize  cell size in source pixels; 32 (assets.TileSize) by default.
//
// Tile-sheet uploads carry cols + tile_size in metadata_json; sprite
// uploads have no metadata and collapse to (1, 32) so the renderer
// treats the whole PNG as a single cell. This helper centralises the
// fallback so detail handlers (initial render + post-component-add
// re-render) and any future surfaces stay consistent.
func spriteRenderInfoFor(d Deps, et *entities.EntityType) (string, int32, int32) {
	if et.SpriteAssetID == nil || d.Assets == nil {
		return "", 1, int32(assets.TileSize)
	}
	a, err := d.Assets.FindByID(context.Background(), *et.SpriteAssetID)
	if err != nil {
		return "", 1, int32(assets.TileSize)
	}
	url := assetPublicURLFunc([]assets.Asset{*a})(a.ContentAddressedPath)
	cols, size := int32(1), int32(assets.TileSize)
	if md, derr := assets.DecodeTileSheetMetadata(a.MetadataJSON); derr == nil && md.Cols > 0 {
		cols = int32(md.Cols)
		if md.TileSize > 0 {
			size = int32(md.TileSize)
		}
	}
	return url, cols, size
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
			CreatedBy: dr.ID,
		})
		// Note: the form's "public" checkbox now belongs to LEVEL, not
		// MAP. Phase 2 reshapes the new-map flow to also offer
		// "create starter level" with the public flag.
		_ = parseCheckbox(r.FormValue("public"))
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
		// Build the tile-palette via the shared helper (see
		// palette_helper.go). Per the holistic redesign, paintable
		// tiles are entity_class='tile'; NPCs/PCs/logic have their
		// own classes and can't be painted here.
		atlas := BuildPaletteAtlas(r.Context(), d, []entities.Class{entities.ClassTile})
		palette := make([]views.PaletteEntry, 0, len(atlas))
		for _, e := range atlas {
			palette = append(palette, views.PaletteEntry{
				ID:                e.ID,
				Name:              e.Name,
				SpriteURL:         e.SpriteURL,
				AtlasIndex:        e.AtlasIndex,
				AtlasCols:         e.AtlasCols,
				TileSize:          e.TileSize,
				ProceduralInclude: e.ProceduralInclude,
				FolderID:          e.FolderID,
			})
		}
		// Pull the tilemap-folder tree once so the view can group
		// entries without a round-trip per folder. Sprite/audio/UI
		// roots are out of scope for the Mapmaker palette by design.
		var paletteFolders []folders.Folder
		if d.Folders != nil {
			fs, ferr := d.Folders.ListByKindRoot(r.Context(), folders.KindTilemap)
			if ferr != nil {
				slog.Warn("palette folder tree", "err", ferr)
			} else {
				paletteFolders = fs
			}
		}
		// Count entity_type drafts so the palette can warn the designer
		// that pending sprite/collider edits aren't visible until they
		// Push to Live. Failure degrades quietly.
		var entityDrafts int
		_ = d.Maps.Pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM drafts WHERE artifact_kind = 'entity_type'`,
		).Scan(&entityDrafts)

		// Count locked cells for the procedural panel chip. Cheap;
		// an indexed COUNT on the map_id fk.
		var lockedCount int
		if m.Mode == "procedural" {
			n, lerr := d.Maps.LockedCellCount(r.Context(), m.ID)
			if lerr != nil {
				slog.Warn("locked cell count", "err", lerr, "map_id", m.ID)
			} else {
				lockedCount = n
			}
		}

		layout := BuildChrome(r, d)
		layout.Title = "Mapmaker · " + m.Name
		layout.Surface = "mapmaker"
		layout.ActiveKind = "map"
		layout.ActiveID = m.ID
		layout.Variant = "bleed"
		layout.BodyClass = "bx-mapmaker-body"

		// Mint a one-shot WS ticket the entry script uses to open
		// the editor's WebSocket. The mapmaker page is designer-
		// realm only so we always have a CurrentDesigner here.
		wsURL := resolveSandboxWSURL(r)
		wsTicket := ""
		if d.Auth != nil {
			if dr := CurrentDesigner(r.Context()); dr != nil {
				if t, err := d.Auth.MintWSTicket(r.Context(), dr.ID, clientIP(r)); err == nil {
					wsTicket = t
				} else {
					slog.Warn("mapmaker mint ws ticket", "err", err)
				}
			}
		}

		renderHTML(w, r, views.MapmakerPage(views.MapmakerProps{
			Layout:             layout,
			Map:                *m,
			Layers:             layers,
			PaletteEntityTypes: palette,
			PaletteFolders:     paletteFolders,
			EntityDraftCount:   entityDrafts,
			LockedCellCount:    lockedCount,
			WSURL:              wsURL,
			WSTicket:           wsTicket,
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
	Width        int32             `json:"width"`
	Height       int32             `json:"height"`
	TileSetSize  int               `json:"tileset_size"`
	Algorithm    string            `json:"algorithm"`
	Fallbacks    int               `json:"fallbacks"`
	PatternCount int               `json:"pattern_count"`
	Cells        []previewCellJSON `json:"cells"`
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
			MapID:             m.ID,
			Width:             width,
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
			Width:        res.Region.Width,
			Height:       res.Region.Height,
			TileSetSize:  res.TileSetSize,
			Algorithm:    res.Algorithm,
			Fallbacks:    res.Fallbacks,
			PatternCount: res.PatternCount,
			Cells:        make([]previewCellJSON, 0, len(res.Region.Cells)),
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

// ---- Procedural: locked cells ----

// lockedCellJSON mirrors mapsservice.LockedCell on the wire.
type lockedCellJSON struct {
	LayerID         int64 `json:"layer_id"`
	X               int32 `json:"x"`
	Y               int32 `json:"y"`
	EntityTypeID    int64 `json:"entity_type_id"`
	RotationDegrees int16 `json:"rotation_degrees,omitempty"`
}

type locksResponse struct {
	MapID int64            `json:"map_id"`
	Count int              `json:"count"`
	Cells []lockedCellJSON `json:"cells"`
}

func getMapLocks(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cells, err := d.Maps.LockedCells(r.Context(), id)
		if err != nil {
			slog.Error("locked cells", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out := locksResponse{MapID: id, Count: len(cells), Cells: make([]lockedCellJSON, 0, len(cells))}
		for _, c := range cells {
			out.Cells = append(out.Cells, lockedCellJSON{
				LayerID: c.LayerID, X: c.X, Y: c.Y,
				EntityTypeID: c.EntityTypeID, RotationDegrees: c.RotationDegrees,
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	}
}

type postLocksRequest struct {
	Cells []lockedCellJSON `json:"cells"`
}

func postMapLocks(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req postLocksRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.Cells) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Reject brushes longer than a sensible safety cap so a runaway
		// drag can't hammer the DB. Designers brushing 4k cells in one
		// stroke means something's broken upstream.
		const maxBrushCells = 4096
		if len(req.Cells) > maxBrushCells {
			http.Error(w, "too many cells in one request", http.StatusRequestEntityTooLarge)
			return
		}
		out := make([]mapsservice.LockedCell, 0, len(req.Cells))
		for _, c := range req.Cells {
			out = append(out, mapsservice.LockedCell{
				MapID: id, LayerID: c.LayerID, X: c.X, Y: c.Y,
				EntityTypeID: c.EntityTypeID, RotationDegrees: c.RotationDegrees,
			})
		}
		if err := d.Maps.LockCells(r.Context(), out); err != nil {
			if errors.Is(err, mapsservice.ErrLockedCellInvalid) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			slog.Error("lock cells", "err", err, "map_id", id, "n", len(out))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type deleteLocksRequest struct {
	LayerID int64    `json:"layer_id"`
	Points  [][2]int32 `json:"points"`
	// All=true wipes every lock on the map (or layer when LayerID is set).
	All bool `json:"all,omitempty"`
}

func deleteMapLocks(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req deleteLocksRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		if req.All {
			if err := d.Maps.ClearLockedCells(r.Context(), id, req.LayerID); err != nil {
				slog.Error("clear locked cells", "err", err, "map_id", id)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if req.LayerID == 0 || len(req.Points) == 0 {
			http.Error(w, "layer_id and points are required (or set all=true)", http.StatusBadRequest)
			return
		}
		if err := d.Maps.UnlockCells(r.Context(), id, req.LayerID, req.Points); err != nil {
			slog.Error("unlock cells", "err", err, "map_id", id, "n", len(req.Points))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- Procedural: sample-patch (overlapping-model WFC source) ----

// samplePatchJSON mirrors maps.SamplePatch for the wire.
type samplePatchJSON struct {
	MapID    int64 `json:"map_id"`
	LayerID  int64 `json:"layer_id"`
	X        int32 `json:"x"`
	Y        int32 `json:"y"`
	Width    int32 `json:"width"`
	Height   int32 `json:"height"`
	PatternN int16 `json:"pattern_n"`
}

func getMapSamplePatch(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := d.Maps.SamplePatchByMap(r.Context(), id)
		if err != nil {
			if errors.Is(err, mapsservice.ErrNoSamplePatch) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			slog.Error("get sample patch", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(samplePatchJSON{
			MapID:    p.MapID,
			LayerID:  p.LayerID,
			X:        p.X,
			Y:        p.Y,
			Width:    p.Width,
			Height:   p.Height,
			PatternN: p.PatternN,
		})
	}
}

func postMapSamplePatch(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req samplePatchJSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Maps.UpsertSamplePatch(r.Context(), mapsservice.SamplePatchInput{
			MapID:    id,
			LayerID:  req.LayerID,
			X:        req.X,
			Y:        req.Y,
			Width:    req.Width,
			Height:   req.Height,
			PatternN: req.PatternN,
		}); err != nil {
			if errors.Is(err, mapsservice.ErrSamplePatchInvalid) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			slog.Error("upsert sample patch", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteMapSamplePatch(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Maps.DeleteSamplePatch(r.Context(), id); err != nil {
			slog.Error("delete sample patch", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- Procedural: non-local constraints (border / path / future) ----

// constraintsResponse mirrors mapsservice.MapConstraint on the wire.
type constraintsResponse struct {
	MapID int64                  `json:"map_id"`
	Items []mapsservice.MapConstraint `json:"items"`
}

func getMapConstraints(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		items, err := d.Maps.MapConstraints(r.Context(), id)
		if err != nil {
			slog.Error("list constraints", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []mapsservice.MapConstraint{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(constraintsResponse{MapID: id, Items: items})
	}
}

// addConstraintRequest mirrors mapsservice.AddMapConstraintInput minus
// the map id (which comes from the path).
type addConstraintRequest struct {
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params"`
}

type addConstraintResponse struct {
	ID int64 `json:"id"`
}

func postMapConstraint(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req addConstraintRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		newID, err := d.Maps.AddMapConstraint(r.Context(), mapsservice.AddMapConstraintInput{
			MapID: id, Kind: req.Kind, Params: req.Params,
		})
		if err != nil {
			if errors.Is(err, mapsservice.ErrConstraintInvalid) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			slog.Error("add constraint", "err", err, "map_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(addConstraintResponse{ID: newID})
	}
}

// postEntityTypeProceduralInclude flips the procedural_include flag on
// one entity type. Powers the eye-icon toggle in the palette.
func postEntityTypeProceduralInclude(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req struct {
			Include bool `json:"include"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Entities.SetProceduralInclude(r.Context(), id, req.Include); err != nil {
			if errors.Is(err, entities.ErrEntityTypeNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("set procedural include", "err", err, "entity_type_id", id)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteMapConstraint(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cidStr := r.PathValue("cid")
		var cid int64
		if _, err := fmt.Sscan(cidStr, &cid); err != nil || cid <= 0 {
			http.Error(w, "invalid constraint id", http.StatusBadRequest)
			return
		}
		if err := d.Maps.DeleteMapConstraint(r.Context(), id, cid); err != nil {
			slog.Error("delete constraint", "err", err, "map_id", id, "cid", cid)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
		// Map-level settings now expose only geometry-related fields
		// (name, mode, seed). Public/instancing/persistence/spectator
		// moved to LEVEL in the holistic redesign; the level editor
		// owns those toggles in Phase 2.
		values := map[string]any{
			"name": m.Name,
			"mode": m.Mode,
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
				if d.Mode != "" {
					values["mode"] = d.Mode
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
			Name: strings.TrimSpace(r.FormValue("name")),
			Mode: strings.TrimSpace(r.FormValue("mode")),
		}
		if v := strings.TrimSpace(r.FormValue("seed")); v != "" {
			if n, err := strconvAtoi64(v); err == nil {
				draft.Seed = &n
			}
		}
		// public/instancing/persistence/spectator/refresh_window are
		// LEVEL fields now — the form may still post them but they
		// land in the level draft, not the map draft.
		_ = parseCheckbox(r.FormValue("public"))
		_ = strings.TrimSpace(r.FormValue("instancing_mode"))
		_ = strings.TrimSpace(r.FormValue("persistence_mode"))
		_ = strings.TrimSpace(r.FormValue("spectator_policy"))
		_ = strings.TrimSpace(r.FormValue("refresh_window_seconds"))
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
		tl, lerr := d.Assets.List(r.Context(), assets.ListOpts{Kind: assets.KindSpriteAnimated, Limit: 200})
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
		// Action groups are level-scoped now. The HUD widget builder
		// runs in a map context; for Phase 1b we surface every action
		// group via a placeholder level lookup. Phase 2 reshapes the
		// HUD page to live under a level.
		gs, lerr := d.ActionGroups.ListByLevel(r.Context(), mapID)
		if lerr != nil {
			return nil, nil, nil, nil, lerr
		}
		for _, g := range gs {
			groupNames = append(groupNames, g.Name)
		}
	}
	return skins, icons, flagKeys, groupNames, nil
}

// getMapHUDPage now serves /design/levels/{id}/hud. The path id is a
// LEVEL id; we resolve the level first to pick up its HUD layout, then
// load its backing map for the editor's preview pane title.
//
// Function name is preserved for git-blame continuity; semantically
// it's the "level HUD page" now.
func getMapHUDPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		levelID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		lv, err := d.Levels.FindByID(r.Context(), levelID)
		if err != nil {
			http.Error(w, "level not found", http.StatusNotFound)
			return
		}
		m, err := d.Maps.FindByID(r.Context(), lv.MapID)
		if err != nil {
			http.Error(w, "level's map not found", http.StatusNotFound)
			return
		}
		if d.HUD == nil {
			http.Error(w, "hud subsystem unavailable", http.StatusServiceUnavailable)
			return
		}
		layout, err := d.HUD.Get(r.Context(), levelID, dr.ID)
		if err != nil {
			if errors.Is(err, hud.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("hud get", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		skins, icons, flagKeys, groupNames, err := d.hudLoadCommonDeps(r, levelID)
		if err != nil {
			slog.Error("hud common deps", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		shell := BuildChrome(r, d)
		shell.Title = "HUD · " + lv.Name
		shell.Surface = "hud-editor"
		shell.ActiveKind = "level"
		shell.ActiveID = lv.ID
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
		//
		// We pass the RAW form value through (not NormalizeUploadKind'd)
		// because the upload service needs the original signal — e.g.
		// "tilemap" and "animated_sprite" both normalize to
		// KindSpriteAnimated, but only the former should drive the
		// auto-create-tilemap path. The service does its own
		// normalization downstream.
		_ = r.ParseMultipartForm(int64(assets.MaxUploadBytes) * int64(assets.MaxFilesPerUpload))
		kindOverride := assets.Kind(firstNonEmpty(
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
				// Tilemap-eligible uploads (multi-cell 32×32 grids)
				// land as a sprite_animated asset PLUS a `tilemaps`
				// row on top. The tilemap service slices the PNG,
				// creates one tile-class entity_type per non-empty
				// cell, and persists per-cell pixel + edge-strip
				// hashes (which power Replace's diff-by-pixel-hash
				// flow + the auto-extracted edge sockets).
				if res.TilemapEligible && len(res.TilemapCells) > 0 && d.Tilemaps != nil {
					n, perr := autoCreateTilemap(r.Context(), d, res, dr.ID)
					if perr != nil {
						slog.Warn("auto-create tilemap",
							"err", perr,
							"asset_id", res.Asset.ID,
						)
					} else {
						item.TileEntityCount = n
					}
				}
				// UI-panel uploads auto-create a matching ClassUI
				// entity_type so the sprite is immediately usable
				// in the editor's chrome AND in player-facing HUDs.
				// Same dogfood loop as tilemap auto-creation; one
				// upload, one row, ready to use.
				if res.Asset.Kind == assets.KindUIPanel && d.Entities != nil {
					if perr := autoCreateUIEntity(r.Context(), d, res.Asset, dr.ID); perr != nil {
						slog.Warn("auto-create ui entity_type",
							"err", perr,
							"asset_id", res.Asset.ID,
						)
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
