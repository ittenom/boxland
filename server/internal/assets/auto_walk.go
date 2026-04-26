// Boxland — sprite-sheet auto-import + walk-animation synthesis.
//
// The plan calls out walk animations as the most-used animation kind and
// asks that they "work fastest of any". The fast path is: a designer
// drops in a top-down character strip — no sidecar — and the four
// directional walks just exist on the asset.
//
// Convention here matches the de-facto top-down RPG layout: a 4-row
// sheet where each row is a facing in the order N, E, S, W (matching
// the EntityState.facing wire encoding). When that shape is detected,
// we emit `walk_north`/`walk_east`/`walk_south`/`walk_west` plus a
// stationary `idle`. Other shapes get a single `idle` covering the
// whole sheet — better than nothing, and still renderable.
//
// Designers who want full control author the sheet with an Aseprite
// sidecar (parse_aseprite.go) and the importer registry's auto-detect
// picks that path instead. This file is the FALLBACK for plain PNGs.

package assets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
)

// AutoSliceConfig drives DefaultSpriteImport when no parser-specific
// config is supplied. Defaults match CanonicalCellPx so the typical
// 32x32 character sheet "just works".
type AutoSliceConfig struct {
	CellW int
	CellH int
}

// DefaultSpriteImport runs the importer registry's auto-detect against
// `body` (and optional sidecar JSON), falling back to a uniform-grid
// raw slice. Always returns a non-nil ImportResult on success — the
// caller persists `Animations` to asset_animations and `SheetMetadata`
// to assets.metadata_json.
//
// Behaviour:
//   - If `sidecar` is non-empty, the registry's auto-detect runs with
//     its bytes and filename hints. Aseprite sidecars hit this path.
//   - If no parser claims the file (the common case for a plain PNG),
//     we slice the image at `cfg.CellW × cfg.CellH` and synthesize
//     walk + idle animations from the layout.
//   - If the image isn't divisible by the cell size, returns
//     ErrParseFailed so the upload handler can surface a clean error.
//
// `assetSize` is used in error messages only.
func DefaultSpriteImport(
	ctx context.Context,
	reg *Registry,
	filename string,
	body []byte,
	sidecar []byte,
	cfg AutoSliceConfig,
) (*ImportResult, error) {
	if cfg.CellW <= 0 {
		cfg.CellW = CanonicalCellPx
	}
	if cfg.CellH <= 0 {
		cfg.CellH = CanonicalCellPx
	}

	// Sidecar path: try to find a parser for the sidecar (Aseprite).
	if len(sidecar) > 0 && reg != nil {
		if imp, ok := reg.AutoDetect(filename, sidecar); ok {
			res, err := imp.Parse(ctx, body, sidecar)
			if err != nil {
				return nil, err
			}
			ensureIdleAnimation(res)
			return res, nil
		}
	}

	// No sidecar (or no parser claimed it): uniform-grid slice.
	res, err := autoSliceUniformGrid(body, cfg.CellW, cfg.CellH)
	if err != nil {
		return nil, err
	}
	res.Animations = SynthesizeWalkAnimations(res.SheetMetadata.Cols, res.SheetMetadata.Rows)
	ensureIdleAnimation(res)
	return res, nil
}

// SynthesizeWalkAnimations returns the canonical animation set for a
// uniform-grid sprite sheet, derived from its (cols, rows) shape. The
// rules:
//
//   - 4 rows, ≥1 col → one row per facing in N/E/S/W order
//     (`walk_north`/`walk_east`/`walk_south`/`walk_west`), plus
//     `idle` at frame 0. This matches the long-standing top-down RPG
//     convention used by RPG Maker, OpenGameArt LPC, and most pixel-
//     art tutorials. Fastest path: drop a 4×N PNG, get four walks.
//   - 1 row, ≥2 cols → a single non-directional `walk` over the row.
//   - anything else → empty (caller's `ensureIdleAnimation` will add
//     a single-frame `idle` so the asset still has *something* the
//     renderer can pull).
//
// FPS defaults to 8, the indie-pixel-art canonical walk cadence (eight
// frames per second on a 4-frame cycle = a nice 2 Hz step rate).
func SynthesizeWalkAnimations(cols, rows int) []Animation {
	if cols < 1 || rows < 1 {
		return nil
	}
	const fps = 8
	if rows == 4 && cols >= 1 {
		c := int(cols)
		// Row-major frame index = row*cols + col. Each row IS one walk.
		return []Animation{
			{Name: AnimWalkN, FrameFrom: 0, FrameTo: c - 1, FPS: fps, Direction: DirForward},
			{Name: AnimWalkE, FrameFrom: c, FrameTo: 2*c - 1, FPS: fps, Direction: DirForward},
			{Name: AnimWalkS, FrameFrom: 2 * c, FrameTo: 3*c - 1, FPS: fps, Direction: DirForward},
			{Name: AnimWalkW, FrameFrom: 3 * c, FrameTo: 4*c - 1, FPS: fps, Direction: DirForward},
			// Idle pose: first frame of the south-facing row (camera-facing).
			{Name: AnimIdle, FrameFrom: 2 * c, FrameTo: 2 * c, FPS: 1, Direction: DirForward},
		}
	}
	if rows == 1 && cols >= 2 {
		c := int(cols)
		return []Animation{
			{Name: AnimWalk, FrameFrom: 0, FrameTo: c - 1, FPS: fps, Direction: DirForward},
			{Name: AnimIdle, FrameFrom: 0, FrameTo: 0, FPS: 1, Direction: DirForward},
		}
	}
	return nil
}

// ensureIdleAnimation appends a single-frame `idle` covering frame 0
// when no animation in the set carries that name. Belt-and-suspenders
// for the runtime: every imported sheet has at least an `idle` to
// fall back to when the requested clip isn't found.
func ensureIdleAnimation(res *ImportResult) {
	if res == nil {
		return
	}
	for _, a := range res.Animations {
		if a.Name == AnimIdle {
			return
		}
	}
	// Pick a reasonable frame for idle: first frame of the sheet.
	res.Animations = append(res.Animations, Animation{
		Name:      AnimIdle,
		FrameFrom: 0,
		FrameTo:   0,
		FPS:       1,
		Direction: DirForward,
	})
}

// autoSliceUniformGrid is the core uniform-grid slicer used by
// DefaultSpriteImport. Returns ImportResult.Frames + SheetMetadata
// populated; Animations is left to the caller to synthesize.
func autoSliceUniformGrid(body []byte, cellW, cellH int) (*ImportResult, error) {
	if cellW <= 0 || cellH <= 0 {
		return nil, errors.New("autoSliceUniformGrid: cell dims must be > 0")
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseFailed, err)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w%cellW != 0 || h%cellH != 0 {
		return nil, fmt.Errorf("%w: image %dx%d is not divisible by cell %dx%d",
			ErrParseFailed, w, h, cellW, cellH)
	}
	cols := w / cellW
	rows := h / cellH
	frames := make([]FrameRect, 0, cols*rows)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			frames = append(frames, FrameRect{
				Index: r*cols + c,
				SX:    c * cellW,
				SY:    r * cellH,
				SW:    cellW,
				SH:    cellH,
			})
		}
	}
	return &ImportResult{
		Frames: frames,
		SheetMetadata: SheetMetadata{
			GridW:      cellW,
			GridH:      cellH,
			Cols:       cols,
			Rows:       rows,
			FrameCount: len(frames),
			Source:     "auto",
		},
	}, nil
}
