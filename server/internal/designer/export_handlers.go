package designer

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"boxland/server/internal/assets"
	"boxland/server/internal/exporter"
	"boxland/server/internal/importer"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/worlds"
	"boxland/server/views"
)

// exporterDeps builds the canonical exporter Deps from the designer
// Deps. Centralized so every export handler ships the same dependency
// graph; new fields land here once and propagate.
func exporterDeps(d Deps) exporter.Deps {
	return exporter.Deps{
		Assets:       d.Assets,
		Entities:     d.Entities,
		Folders:      d.Folders,
		Tilemaps:     d.Tilemaps,
		Maps:         d.Maps,
		Levels:       d.Levels,
		Worlds:       d.Worlds,
		ActionGroups: d.ActionGroups,
		ObjectStore:  d.ObjectStore,
	}
}

// importerDeps mirrors exporterDeps for the import side.
func importerDeps(d Deps) importer.Deps {
	return importer.Deps{
		Assets:       d.Assets,
		Entities:     d.Entities,
		Folders:      d.Folders,
		Tilemaps:     d.Tilemaps,
		Maps:         d.Maps,
		Levels:       d.Levels,
		Worlds:       d.Worlds,
		ActionGroups: d.ActionGroups,
		ObjectStore:  d.ObjectStore,
	}
}

// ---- Export handlers --------------------------------------------------

// getMapExport streams a single map packaged as a .boxmap.zip back to
// the browser. The button on the Mapmaker page is a plain `<a download>`
// that hits this URL, so the response goes straight into the OS file
// dialog.
func getMapExport(d Deps) http.HandlerFunc {
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
		// Tenant guard: only the designer who owns the map can export
		// it (matches the per-realm HUD scoping). One-tenant build
		// per server, but we still gate so a future multi-tenant
		// migration doesn't leak via this endpoint.
		if m.CreatedBy != dr.ID {
			http.NotFound(w, r)
			return
		}
		exp := exporter.New(exporterDeps(d))
		bytes, err := exp.ExportMap(r.Context(), mapID, dr.ID)
		if err != nil {
			slog.Error("export map", "err", err, "map_id", mapID, "designer_id", dr.ID)
			http.Error(w, "export map: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeZipDownload(w, exporter.FilenameFor(exporter.KindMap, m.Name, time.Now()), bytes)
	}
}

// getAssetExport streams one asset as a .boxasset.zip.
func getAssetExport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		idStr := r.PathValue("id")
		assetID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || assetID <= 0 {
			http.NotFound(w, r)
			return
		}
		a, err := d.Assets.FindByID(r.Context(), assetID)
		if err != nil {
			if errors.Is(err, assets.ErrAssetNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "find asset: "+err.Error(), http.StatusInternalServerError)
			return
		}
		exp := exporter.New(exporterDeps(d))
		bytes, err := exp.ExportAsset(r.Context(), assetID, dr.ID)
		if err != nil {
			slog.Error("export asset", "err", err, "asset_id", assetID, "designer_id", dr.ID)
			http.Error(w, "export asset: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeZipDownload(w, exporter.FilenameFor(exporter.KindAsset, a.Name, time.Now()), bytes)
	}
}

// getAllAssetsExport streams every asset as a .boxassets.zip. Useful
// for project backups + migration between dev/prod databases.
func getAllAssetsExport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		exp := exporter.New(exporterDeps(d))
		bytes, err := exp.ExportAllAssets(r.Context(), dr.ID)
		if err != nil {
			slog.Error("export all assets", "err", err, "designer_id", dr.ID)
			http.Error(w, "export assets: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeZipDownload(w, exporter.FilenameFor(exporter.KindAllAssets, "", time.Now()), bytes)
	}
}

// ---- Import handlers --------------------------------------------------

// getAssetsImportModal renders the small file-picker modal that powers
// the "Import" button on the Library list page. The form posts to
// /design/assets/import; the result lands in #modal-host as a summary
// toast.
func getAssetsImportModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderHTML(w, r, views.AssetsImportModal())
	}
}

// getTilemapImportModal renders the import file-picker for tilemaps.
func getTilemapImportModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderHTML(w, r, views.ImportModal(views.ImportModalProps{
			Title:   "Import tilemap",
			Lede:    "Drop a .boxtilemap file here. The tilemap, its tile entities, and the backing PNG come along together.",
			PostURL: "/design/tilemaps/import",
			Accept:  ".zip,.boxtilemap,application/zip",
		}))
	}
}

// getLevelImportModal renders the import file-picker for levels.
func getLevelImportModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderHTML(w, r, views.ImportModal(views.ImportModalProps{
			Title:   "Import level",
			Lede:    "Drop a .boxlevel file here. The level, its backing map, entity placements, HUD, and assets come along together.",
			PostURL: "/design/levels/import",
			Accept:  ".zip,.boxlevel,application/zip",
		}))
	}
}

// getWorldImportModal renders the import file-picker for worlds.
func getWorldImportModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderHTML(w, r, views.ImportModal(views.ImportModalProps{
			Title:   "Import world",
			Lede:    "Drop a .boxworld file here. Every level, map, tilemap, and asset in the world comes along together.",
			PostURL: "/design/worlds/import",
			Accept:  ".zip,.boxworld,application/zip",
		}))
	}
}


// postAssetsImport accepts a multipart upload of a .boxasset(s).zip.
// Loads the bytes, validates the manifest, applies asset rows + blob
// uploads, and returns a JSON or HTMX-friendly summary.
//
// Scope: 64 MiB cap per upload (4× the per-file asset cap). A typical
// project export with bundled blobs lands well under that.
func postAssetsImport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		body, err := readImportBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		policy := importer.ConflictPolicy(r.URL.Query().Get("policy"))
		imp := importer.New(importerDeps(d))
		res, err := imp.ImportAssets(r.Context(), body, dr.ID, policy)
		if err != nil {
			slog.Warn("import assets", "err", err, "designer_id", dr.ID)
			http.Error(w, err.Error(), importErrStatus(err))
			return
		}
		writeImportResult(w, r, res)
	}
}

// postMapImport accepts a multipart upload of a .boxmap.zip and
// applies it as a fresh map (never overwrites a live map).
func postMapImport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		body, err := readImportBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		policy := importer.ConflictPolicy(r.URL.Query().Get("policy"))
		imp := importer.New(importerDeps(d))
		res, err := imp.ImportMap(r.Context(), body, dr.ID, policy)
		if err != nil {
			slog.Warn("import map", "err", err, "designer_id", dr.ID)
			http.Error(w, err.Error(), importErrStatus(err))
			return
		}
		writeImportResult(w, r, res)
	}
}

// ---- helpers ----------------------------------------------------------

// MaxImportBytes caps a single import upload. 64 MiB carries plenty of
// PNG sheets + audio for a real project; bigger projects can split
// across multiple imports.
const MaxImportBytes = 64 * 1024 * 1024

// readImportBody pulls the first file from a multipart form (`file`
// field) and returns its bytes. Mirrors the asset upload path.
func readImportBody(r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxImportBytes+1)
	if err := r.ParseMultipartForm(MaxImportBytes); err != nil {
		return nil, fmt.Errorf("parse multipart: %w", err)
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, MaxImportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > MaxImportBytes {
		return nil, fmt.Errorf("file exceeds %d byte limit", MaxImportBytes)
	}
	return body, nil
}

// writeZipDownload writes a zip body with the right Content-Type and
// Content-Disposition for a browser-driven file save.
func writeZipDownload(w http.ResponseWriter, filename string, body []byte) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	// The export is freshly assembled per request; never let an
	// upstream cache it (designer data + a presigned-style timestamp
	// in the manifest).
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	_, _ = w.Write(body)
}

// writeImportResult renders the import summary either as a small HTMX
// fragment (toast) or as JSON (programmatic).
func writeImportResult(w http.ResponseWriter, r *http.Request, res *importer.Result) {
	if r.Header.Get("HX-Request") == "true" {
		renderHTML(w, r, views.ImportSummary(views.ImportSummaryProps{
			AssetsCreated:      res.AssetsCreated,
			AssetsSkipped:      res.AssetsSkipped,
			BlobsUploaded:      res.BlobsUploaded,
			EntityTypesCreated: res.EntityTypesCreated,
			EntityTypesSkipped: res.EntityTypesSkipped,
			TilemapsCreated:    res.TilemapsCreated,
			TilemapsSkipped:    res.TilemapsSkipped,
			MapsCreated:        res.MapsCreated,
			LevelsCreated:      res.LevelsCreated,
			WorldsCreated:      res.WorldsCreated,
			Warnings:           res.Warnings,
		}))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- Phase 3 export handlers (tilemap / level / world) ----------------

// getTilemapExport streams one tilemap as a .boxtilemap.zip.
func getTilemapExport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		if d.Tilemaps == nil {
			http.Error(w, "tilemaps service unavailable", http.StatusServiceUnavailable)
			return
		}
		tm, err := d.Tilemaps.FindByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, tilemaps.ErrTilemapNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "find tilemap: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if tm.CreatedBy != dr.ID {
			http.NotFound(w, r)
			return
		}
		exp := exporter.New(exporterDeps(d))
		bytes, err := exp.ExportTilemap(r.Context(), id, dr.ID)
		if err != nil {
			slog.Error("export tilemap", "err", err, "tilemap_id", id, "designer_id", dr.ID)
			http.Error(w, "export tilemap: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeZipDownload(w, exporter.FilenameFor(exporter.KindTilemap, tm.Name, time.Now()), bytes)
	}
}

// getLevelExport streams one level as a .boxlevel.zip.
func getLevelExport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		lv, err := d.Levels.FindByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, levels.ErrLevelNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "find level: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if lv.CreatedBy != dr.ID {
			http.NotFound(w, r)
			return
		}
		exp := exporter.New(exporterDeps(d))
		bytes, err := exp.ExportLevel(r.Context(), id, dr.ID)
		if err != nil {
			slog.Error("export level", "err", err, "level_id", id, "designer_id", dr.ID)
			http.Error(w, "export level: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeZipDownload(w, exporter.FilenameFor(exporter.KindLevel, lv.Name, time.Now()), bytes)
	}
}

// getWorldExport streams one world as a .boxworld.zip.
func getWorldExport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		if d.Worlds == nil {
			http.Error(w, "worlds service unavailable", http.StatusServiceUnavailable)
			return
		}
		wld, err := d.Worlds.FindByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, worlds.ErrWorldNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "find world: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if wld.CreatedBy != dr.ID {
			http.NotFound(w, r)
			return
		}
		exp := exporter.New(exporterDeps(d))
		bytes, err := exp.ExportWorld(r.Context(), id, dr.ID)
		if err != nil {
			slog.Error("export world", "err", err, "world_id", id, "designer_id", dr.ID)
			http.Error(w, "export world: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeZipDownload(w, exporter.FilenameFor(exporter.KindWorld, wld.Name, time.Now()), bytes)
	}
}

// postTilemapImport accepts a .boxtilemap.zip upload.
func postTilemapImport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		body, err := readImportBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		policy := importer.ConflictPolicy(r.URL.Query().Get("policy"))
		imp := importer.New(importerDeps(d))
		res, err := imp.ImportTilemap(r.Context(), body, dr.ID, policy)
		if err != nil {
			slog.Warn("import tilemap", "err", err, "designer_id", dr.ID)
			http.Error(w, err.Error(), importErrStatus(err))
			return
		}
		writeImportResult(w, r, res)
	}
}

// postLevelImport accepts a .boxlevel.zip upload.
func postLevelImport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		body, err := readImportBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		policy := importer.ConflictPolicy(r.URL.Query().Get("policy"))
		imp := importer.New(importerDeps(d))
		res, err := imp.ImportLevel(r.Context(), body, dr.ID, policy)
		if err != nil {
			slog.Warn("import level", "err", err, "designer_id", dr.ID)
			http.Error(w, err.Error(), importErrStatus(err))
			return
		}
		writeImportResult(w, r, res)
	}
}

// postWorldImport accepts a .boxworld.zip upload.
func postWorldImport(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		body, err := readImportBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		policy := importer.ConflictPolicy(r.URL.Query().Get("policy"))
		imp := importer.New(importerDeps(d))
		res, err := imp.ImportWorld(r.Context(), body, dr.ID, policy)
		if err != nil {
			slog.Warn("import world", "err", err, "designer_id", dr.ID)
			http.Error(w, err.Error(), importErrStatus(err))
			return
		}
		writeImportResult(w, r, res)
	}
}

// importErrStatus maps importer errors to HTTP status codes.
func importErrStatus(err error) int {
	switch {
	case errors.Is(err, importer.ErrBadZip),
		errors.Is(err, importer.ErrMissingManifest),
		errors.Is(err, importer.ErrUnknownKind),
		errors.Is(err, importer.ErrUnsupportedFmt),
		errors.Is(err, importer.ErrZipSlip),
		errors.Is(err, importer.ErrConflict):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
