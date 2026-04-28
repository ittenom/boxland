package designer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"boxland/server/internal/assets"
	"boxland/server/internal/folders"
	"boxland/server/views"
)

// ---- Folder CRUD -----------------------------------------------------

// postFolderCreate creates a new folder under (kind_root, parent_id).
// Form fields: name, kind_root, parent_id (optional).
//
// Response: HTMX renders the updated tree fragment for the affected
// kind_root; programmatic callers get the new folder JSON.
func postFolderCreate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var parentID *int64
		if s := strings.TrimSpace(r.FormValue("parent_id")); s != "" {
			id, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				http.Error(w, "bad parent_id", http.StatusBadRequest)
				return
			}
			parentID = &id
		}
		f, err := d.Folders.Create(r.Context(), folders.CreateInput{
			Name:      r.FormValue("name"),
			KindRoot:  folders.KindRoot(r.FormValue("kind_root")),
			ParentID:  parentID,
			CreatedBy: dr.ID,
		})
		if err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		respondTree(w, r, d, f.KindRoot, f)
	}
}

// postFolderRename renames a folder. Form field: name.
func postFolderRename(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := folderIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f, err := d.Folders.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		if err := d.Folders.Rename(r.Context(), id, r.FormValue("name")); err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		respondTree(w, r, d, f.KindRoot, f)
	}
}

// postFolderMove reparents a folder. Form fields: parent_id (empty
// string = move to kind root).
func postFolderMove(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := folderIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var parentID *int64
		if s := strings.TrimSpace(r.FormValue("parent_id")); s != "" {
			pid, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				http.Error(w, "bad parent_id", http.StatusBadRequest)
				return
			}
			parentID = &pid
		}
		f, err := d.Folders.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		if err := d.Folders.Move(r.Context(), id, parentID); err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		respondTree(w, r, d, f.KindRoot, f)
	}
}

// postFolderSortMode flips the folder's persisted sort mode.
func postFolderSortMode(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := folderIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mode := folders.SortMode(strings.TrimSpace(r.FormValue("mode")))
		if err := d.Folders.SetSortMode(r.Context(), id, mode); err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		// Sort-mode changes don't reshape the tree, so we return the
		// folder's NEW contents instead. Lazy-backfill dominant_color
		// if needed.
		if mode == folders.SortColor && d.ObjectStore != nil {
			go func() {
				bg := context.Background()
				_, _ = d.Assets.EnsureDominantColors(bg, blobGetter(d), 200)
			}()
		}
		respondContents(w, r, d, &id, "", string(mode))
	}
}

// deleteFolder removes a folder. Children cascade; assets bubble back
// to the kind root.
func deleteFolder(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := folderIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f, err := d.Folders.FindByID(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		if err := d.Folders.Delete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		respondTree(w, r, d, f.KindRoot, nil)
	}
}

// ---- Tree + contents (read-only) -------------------------------------

// getFolderNewModal renders the inline "New folder" picker. Query
// params seed the kind_root + parent_id hidden inputs.
func getFolderNewModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kr := strings.TrimSpace(r.URL.Query().Get("kind_root"))
		if kr == "" || !folders.KindRoot(kr).Valid() {
			http.Error(w, "bad kind_root", http.StatusBadRequest)
			return
		}
		parent := strings.TrimSpace(r.URL.Query().Get("parent_id"))
		renderHTML(w, r, views.NewFolderModal(views.NewFolderModalProps{
			KindRoot:   kr,
			ParentID:   parent,
			Suggestion: strings.TrimSpace(r.URL.Query().Get("name")),
		}))
	}
}


// getFolderTree renders the rail for one or all kind_roots. Query:
// `?kind=tile` for one root; no kind → all four.
func getFolderTree(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := folders.KindRoot(strings.TrimSpace(r.URL.Query().Get("kind")))
		if kind != "" && !kind.Valid() {
			http.Error(w, "bad kind", http.StatusBadRequest)
			return
		}
		props, err := buildFolderTreeProps(r.Context(), d, kind, nil)
		if err != nil {
			slog.Error("folder tree", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderHTML(w, r, views.FolderTree(props))
	}
}

// getFolderContents returns the ordered list of assets for one folder
// (or one kind root if folder_id is empty + kind is set). Query:
// folder_id, kind, sort.
func getFolderContents(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var folderID *int64
		if s := strings.TrimSpace(r.URL.Query().Get("folder_id")); s != "" {
			id, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				http.Error(w, "bad folder_id", http.StatusBadRequest)
				return
			}
			folderID = &id
		}
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		sort := strings.TrimSpace(r.URL.Query().Get("sort"))
		respondContents(w, r, d, folderID, kind, sort)
	}
}

// ---- Asset move / rename ---------------------------------------------

// postAssetsMove bulk-moves assets to a target folder (or kind root if
// folder_id is empty). Form: ids (comma-separated), folder_id.
func postAssetsMove(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ids := parseCommaIDs(firstNonEmpty(r.FormValue("ids"), r.URL.Query().Get("ids")))
		if len(ids) == 0 {
			http.Error(w, "no ids", http.StatusBadRequest)
			return
		}
		var folderID *int64
		if s := strings.TrimSpace(r.FormValue("folder_id")); s != "" {
			id, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				http.Error(w, "bad folder_id", http.StatusBadRequest)
				return
			}
			folderID = &id
		}
		moved, err := d.Folders.MoveAssets(r.Context(), ids, folderID)
		if err != nil {
			http.Error(w, err.Error(), folderErrStatus(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"moved": moved})
	}
}

// postAssetRename renames a single asset. Form: name.
func postAssetRename(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := assetIDFromPath(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Assets.Rename(r.Context(), id, r.FormValue("name")); err != nil {
			http.Error(w, err.Error(), assetRenameErrStatus(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// ---- helpers ----------------------------------------------------------

// folderIDFromPath pulls the {id} path value and parses it.
func folderIDFromPath(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("bad id")
	}
	return id, nil
}

// folderErrStatus maps service errors to HTTP status codes.
func folderErrStatus(err error) int {
	switch {
	case errors.Is(err, folders.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, folders.ErrNameInUse),
		errors.Is(err, folders.ErrCycle),
		errors.Is(err, folders.ErrCrossKindMove),
		errors.Is(err, folders.ErrInvalidKindRoot),
		errors.Is(err, folders.ErrInvalidSortMode),
		errors.Is(err, folders.ErrInvalidName):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func assetRenameErrStatus(err error) int {
	switch {
	case errors.Is(err, assets.ErrAssetNotFound):
		return http.StatusNotFound
	case errors.Is(err, assets.ErrNameInUse):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// blobGetter returns a closure that pulls bytes from object storage,
// shaped for assets.EnsureDominantColors.
func blobGetter(d Deps) func(ctx context.Context, contentPath string) ([]byte, error) {
	return func(ctx context.Context, p string) ([]byte, error) {
		rc, err := d.ObjectStore.Get(ctx, p)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return readAllCapped(rc, 16*1024*1024)
	}
}

// readAllCapped reads at most `cap` bytes — defensive against a bogus
// blob being substituted for a tiny PNG.
func readAllCapped(rc interface{ Read(p []byte) (int, error) }, cap int) ([]byte, error) {
	buf := make([]byte, 0, 32*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, err := rc.Read(tmp)
		if n > 0 {
			if len(buf)+n > cap {
				return buf, errors.New("blob too large")
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}

// respondTree returns the folder tree for one kind_root. HTMX swaps
// it into the rail; programmatic callers get JSON.
func respondTree(w http.ResponseWriter, r *http.Request, d Deps, kr folders.KindRoot, focused *folders.Folder) {
	props, err := buildFolderTreeProps(r.Context(), d, kr, focused)
	if err != nil {
		slog.Error("folder tree", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		renderHTML(w, r, views.FolderTreeRoot(props, kr))
		return
	}
	if focused != nil {
		writeJSON(w, http.StatusOK, focused)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// respondContents returns the asset list inside one folder. HTMX
// renders a grid fragment; programmatic callers get JSON.
func respondContents(w http.ResponseWriter, r *http.Request, d Deps, folderID *int64, kind, sort string) {
	if sort == "" {
		// If the request targets a real folder, prefer the folder's
		// stored sort_mode. Falling back to alpha keeps the behavior
		// stable when the caller doesn't know the folder yet.
		if folderID != nil {
			if f, err := d.Folders.FindByID(r.Context(), *folderID); err == nil {
				sort = string(f.SortMode)
			}
		}
		if sort == "" {
			sort = string(folders.SortAlpha)
		}
	}
	if folderID == nil && kind == "" {
		http.Error(w, "kind required when folder_id is empty", http.StatusBadRequest)
		return
	}
	items, err := d.Assets.ListByFolder(r.Context(), folderID, kind, sort)
	if err != nil {
		slog.Error("list by folder", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	publicURL := assetPublicURLFunc(items)
	if r.Header.Get("HX-Request") == "true" {
		renderHTML(w, r, views.FolderContents(views.FolderContentsProps{
			Items:    items,
			Sort:     sort,
			FolderID: folderID,
			Kind:     kind,
			PublicURL: publicURL,
		}))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": items,
		"sort":  sort,
	})
}

// buildFolderTreeProps gathers folders for one or all kind_roots and
// shapes the props used by the rail templ. Fan-out is bounded by the
// number of kind_roots × folder count; in practice the rail is small.
//
// Tolerates a nil Folders service (test fixtures + minimal embeddings)
// — the rail simply renders empty roots in that case, which is fine.
func buildFolderTreeProps(ctx context.Context, d Deps, kr folders.KindRoot, focused *folders.Folder) (views.FolderTreeProps, error) {
	roots := folders.AllKindRoots()
	if kr != "" {
		roots = []folders.KindRoot{kr}
	}
	out := views.FolderTreeProps{
		Roots: make([]views.FolderRoot, 0, len(roots)),
	}
	for _, kr := range roots {
		var fs []folders.Folder
		if d.Folders != nil {
			loaded, err := d.Folders.ListByKindRoot(ctx, kr)
			if err != nil {
				return out, err
			}
			fs = loaded
		}
		// Count of assets in the kind root for the badge.
		var kindCount int
		if d.Assets != nil && d.Assets.Pool != nil {
			_ = d.Assets.Pool.QueryRow(ctx,
				`SELECT count(*) FROM assets WHERE kind = $1`, string(kr),
			).Scan(&kindCount)
		}

		out.Roots = append(out.Roots, views.FolderRoot{
			KindRoot:   string(kr),
			Label:      humanKindLabel(kr),
			Folders:    fs,
			AssetCount: kindCount,
		})
	}
	if focused != nil {
		out.FocusedID = focused.ID
	}
	return out, nil
}

func humanKindLabel(kr folders.KindRoot) string {
	switch kr {
	case folders.KindSprite:
		return "Sprites"
	case folders.KindTilemap:
		return "Tilemaps"
	case folders.KindAudio:
		return "Audio"
	case folders.KindUIPanel:
		return "UI"
	case folders.KindLevel:
		return "Levels"
	case folders.KindWorld:
		return "Worlds"
	}
	return string(kr)
}
