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
	mapsservice "boxland/server/internal/maps"
	"boxland/server/views"
)

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
		exp := exporter.New(exporter.Deps{
			Assets:      d.Assets,
			Entities:    d.Entities,
			Folders:     d.Folders,
			Maps:        d.Maps,
			ObjectStore: d.ObjectStore,
		})
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
		exp := exporter.New(exporter.Deps{
			Assets:      d.Assets,
			Folders:     d.Folders,
			ObjectStore: d.ObjectStore,
		})
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
		exp := exporter.New(exporter.Deps{
			Assets:      d.Assets,
			Folders:     d.Folders,
			ObjectStore: d.ObjectStore,
		})
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
// the "Import" button on the Asset Manager list page. The form posts
// to /design/assets/import; the result lands in #modal-host as a
// summary toast.
func getAssetsImportModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderHTML(w, r, views.AssetsImportModal())
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
		imp := importer.New(importer.Deps{
			Assets:      d.Assets,
			Entities:    d.Entities,
			Folders:     d.Folders,
			Maps:        d.Maps,
			ObjectStore: d.ObjectStore,
		})
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
		imp := importer.New(importer.Deps{
			Assets:      d.Assets,
			Entities:    d.Entities,
			Folders:     d.Folders,
			Maps:        d.Maps,
			ObjectStore: d.ObjectStore,
		})
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
			MapsCreated:        res.MapsCreated,
			Warnings:           res.Warnings,
		}))
		return
	}
	writeJSON(w, http.StatusOK, res)
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
