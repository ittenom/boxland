// Package setup hosts one-time bootstrap routines: schema seeding,
// asset-pack imports, default-content provisioning. Subcommands of
// `boxland seed` invoke functions here.
package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strings"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/persistence"
	"boxland/server/static"
)

// UIKitDeps is the narrow dependency set ImportUIKitGradient needs.
// Keep this minimal so callers can wire a fresh DB easily during
// `boxland seed` and so tests can stand up the import in isolation.
type UIKitDeps struct {
	Assets   *assets.Service
	Entities *entities.Service
	Store    *persistence.ObjectStore
}

// UIKitImportResult summarizes a seeding run for the operator's
// console output: how many sprites we touched, how many were
// freshly-created, how many were already present.
type UIKitImportResult struct {
	Total    int
	Created  int
	Skipped  int // dedup hits (rerunning the seeder is a no-op)
	Failed   int // errors per sprite; the seeder logs and continues
}

// ImportUIKitGradient walks the embedded `img/ui/gradient/` directory,
// uploads each PNG into object storage at its content-addressed key,
// and creates a matching pair of rows: an `assets` row (Kind=ui_panel)
// and an `entity_types` row (Class=ui) with a `nine_slice` component
// populated from a per-sprite measurement.
//
// Idempotent: re-running the seeder against a populated DB is a no-op.
// We pre-flight by content path on the assets table; existing matches
// short-circuit the upload + create.
//
// The `createdBy` argument is the designer id we attribute the seeded
// rows to. The `boxland seed` subcommand uses the first owner-role
// designer in the system; tests pass a designer id created by their
// fixture.
func ImportUIKitGradient(ctx context.Context, deps UIKitDeps, createdBy int64) (UIKitImportResult, error) {
	if deps.Assets == nil || deps.Entities == nil || deps.Store == nil {
		return UIKitImportResult{}, errors.New("uipack: Assets, Entities, and Store deps are all required")
	}
	dir := path.Join("img", "ui", "gradient")
	files, err := listEmbeddedPNGs(static.FS, dir)
	if err != nil {
		return UIKitImportResult{}, fmt.Errorf("uipack: list embedded sprites: %w", err)
	}
	res := UIKitImportResult{Total: len(files)}

	for _, name := range files {
		fp := path.Join(dir, name)
		body, err := fs.ReadFile(static.FS, fp)
		if err != nil {
			slog.Warn("uipack: read sprite", "file", fp, "err", err)
			res.Failed++
			continue
		}
		dims, derr := readPNGDimensions(body)
		if derr != nil {
			slog.Warn("uipack: decode sprite", "file", fp, "err", derr)
			res.Failed++
			continue
		}
		sliceCfg := MeasureNineSlice(name, dims.Width, dims.Height)

		// Stable name + content-addressed key. Re-runs hit the dedup
		// branch via FindByContentPath.
		caPath := persistence.ContentAddressedKey("ui-pack", body)
		assetName := canonicalAssetName(name)
		entityName := canonicalEntityName(name)

		if existing, err := deps.Assets.FindByContentPath(ctx, assets.KindUIPanel, caPath); err == nil && existing != nil {
			res.Skipped++
			// Even when the asset already exists, ensure the
			// entity_type also exists. Manually deleting the entity
			// shouldn't permanently break the import — re-running
			// the seeder must repair the row.
			if err := ensureUIEntity(ctx, deps, existing.ID, entityName, sliceCfg, createdBy); err != nil {
				slog.Warn("uipack: ensure entity for existing asset", "file", fp, "err", err)
				res.Failed++
			}
			continue
		}

		if err := deps.Store.Put(ctx, caPath, "image/png", bytes.NewReader(body), int64(len(body))); err != nil {
			slog.Warn("uipack: object store put", "file", fp, "err", err)
			res.Failed++
			continue
		}
		// Pack the sprite's pixel dimensions into MetadataJSON so
		// the renderer can size 1:1 thumbnails without re-decoding
		// the PNG client-side.
		md, _ := json.Marshal(uiPackAssetMetadata{
			Width:  dims.Width,
			Height: dims.Height,
			Source: "crusenho-gradient",
		})
		a, err := deps.Assets.Create(ctx, assets.CreateInput{
			Kind:                 assets.KindUIPanel,
			Name:                 assetName,
			ContentAddressedPath: caPath,
			OriginalFormat:       "png",
			MetadataJSON:         md,
			Tags:                 []string{"ui-pack", "crusenho-gradient"},
			CreatedBy:            createdBy,
		})
		if err != nil {
			// Name conflict means a different asset already claims
			// this name. The seeder is idempotent on bytes, not on
			// names; surface the conflict and skip.
			if errors.Is(err, assets.ErrNameInUse) {
				slog.Warn("uipack: asset name in use, skipping", "file", fp, "name", assetName)
				res.Skipped++
				continue
			}
			slog.Warn("uipack: create asset", "file", fp, "err", err)
			res.Failed++
			continue
		}
		if err := ensureUIEntity(ctx, deps, a.ID, entityName, sliceCfg, createdBy); err != nil {
			slog.Warn("uipack: create entity", "file", fp, "err", err)
			res.Failed++
			continue
		}
		res.Created++
	}

	return res, nil
}

// uiPackAssetMetadata is the JSON shape stored on each ui_panel
// asset's metadata_json field. Renderers can decode this without a
// type assertion; the `source` tag flags the upstream pack so future
// kits (Crusenho's blue, paper, neon themes) can coexist.
type uiPackAssetMetadata struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Source string `json:"source"`
}

// ensureUIEntity is the "create or repair" half of the seeder. We
// look for an existing ClassUI entity_type pointing at the asset; if
// we find one, we leave it alone (the operator may have edited the
// insets). If we don't find one, we create it with the measured
// insets and a `nine_slice` component.
func ensureUIEntity(
	ctx context.Context,
	deps UIKitDeps,
	assetID int64,
	entityName string,
	slice components.NineSlice,
	createdBy int64,
) error {
	// FindBySpriteAtlas returns every entity_type pointing at this
	// asset; ClassUI is what we care about. The same asset *could*
	// be referenced by a tile or logic entity in unusual cases, so
	// we only short-circuit when a ClassUI row already exists.
	if existing, err := deps.Entities.FindBySpriteAtlas(ctx, assetID); err == nil {
		for _, e := range existing {
			if e.EntityClass == entities.ClassUI {
				// Leave existing rows alone — the operator may
				// have tweaked the insets; we don't want to
				// clobber that work.
				return nil
			}
		}
	}
	id := assetID
	et, err := deps.Entities.Create(ctx, entities.CreateInput{
		Name:          entityName,
		EntityClass:   entities.ClassUI,
		SpriteAssetID: &id,
		AtlasIndex:    0,
		Tags:          []string{"ui-pack", "crusenho-gradient"},
		CreatedBy:     createdBy,
	})
	if err != nil {
		if errors.Is(err, entities.ErrNameInUse) {
			// Name collision means an existing entity has the same
			// name (perhaps from a prior run that got partially
			// rolled back). The operator can rename it; we don't
			// silently steal the name.
			return nil
		}
		return fmt.Errorf("create ui entity_type: %w", err)
	}
	cfg, err := json.Marshal(slice)
	if err != nil {
		return fmt.Errorf("marshal nine_slice: %w", err)
	}
	if err := deps.Entities.SetComponents(ctx, nil, et.ID, map[components.Kind]json.RawMessage{
		components.KindNineSlice: cfg,
	}); err != nil {
		return fmt.Errorf("set nine_slice component: %w", err)
	}
	return nil
}

// canonicalAssetName turns "UI_Gradient_Button_Large_Release_01a1.png"
// into a stable lower-snake name like "ui_gradient_button_large_release_01a1".
// The asset-name uniqueness constraint is per-kind, so we can re-use
// names across kinds without issue, but consistent naming makes the
// browse surfaces (Library / Entity tree) readable.
func canonicalAssetName(filename string) string {
	stem := strings.TrimSuffix(filename, ".png")
	return strings.ToLower(stem)
}

// canonicalEntityName mirrors canonicalAssetName but adds the entity
// suffix that disambiguates asset rows from entity_type rows when
// they're displayed side-by-side. Convention: lower_snake; same shape
// the auto-create-on-upload path uses (asset.Name verbatim).
func canonicalEntityName(filename string) string {
	return canonicalAssetName(filename)
}

// listEmbeddedPNGs returns the alphabetically-sorted list of *.png
// files directly inside `dir`. Sub-directories are ignored — the kit
// is flat on purpose so the seeder doesn't need to recurse.
func listEmbeddedPNGs(efs fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(efs, dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// pngDimensions is the bag readPNGDimensions returns.
type pngDimensions struct {
	Width  int
	Height int
}

// readPNGDimensions decodes only the PNG header, not the pixel data.
// We use image/png's full Decode here for simplicity; the kit is
// small (<1 MB total) so the perf cost is negligible.
func readPNGDimensions(body []byte) (pngDimensions, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return pngDimensions{}, err
	}
	return pngDimensions{Width: cfg.Width, Height: cfg.Height}, nil
}
