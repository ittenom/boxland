// Boxland — sprite-sheet import pipeline.
//
// An Importer reads an uploaded sheet (already in object storage) plus
// optional sidecar metadata and produces the animation rows we persist
// into asset_animations. The sheet is parsed once at import time; runtime
// frame lookups read the persisted rows, never re-parse the source.
//
// Each parser registers under a stable id ("aseprite", "raw", ...). The
// upload UI offers an explicit dropdown so designers can override a
// failed auto-detect.

package assets

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// Direction matches the asset_animations.direction CHECK constraint.
type Direction string

const (
	DirForward  Direction = "forward"
	DirReverse  Direction = "reverse"
	DirPingpong Direction = "pingpong"
)

// FrameRect describes one frame's rectangle inside the sheet, in texture pixels.
type FrameRect struct {
	Index int // 0-based
	SX    int
	SY    int
	SW    int
	SH    int
	// Optional anchor offset within the frame (Aseprite slices, etc.). Zero
	// = sprite anchored at top-left.
	AX int
	AY int
}

// Animation is one named animation tag (e.g. "walk_north" frames 0..3).
type Animation struct {
	Name      string
	FrameFrom int
	FrameTo   int
	Direction Direction
	FPS       int
}

// ImportResult is what a parser returns. Frames is informational
// (not persisted in v1; runtime regenerates from grid metadata stored on
// the asset's metadata_json). Animations land in asset_animations.
type ImportResult struct {
	Frames     []FrameRect
	Animations []Animation
	// SheetMetadata is folded into assets.metadata_json by the upload
	// pipeline so the renderer's AssetCatalog can rebuild frame rects
	// without re-parsing the source.
	SheetMetadata SheetMetadata
}

// SheetMetadata captures everything the runtime needs about a sheet's
// layout. Stored as the assets.metadata_json payload for sprite/tile assets.
type SheetMetadata struct {
	GridW       int    `json:"grid_w"`        // frame width in pixels
	GridH       int    `json:"grid_h"`        // frame height in pixels
	Cols        int    `json:"cols"`          // number of frames per row
	Rows        int    `json:"rows"`          // number of rows in the sheet
	FrameCount  int    `json:"frame_count"`   // total frames
	Source      string `json:"source"`        // parser id that produced this row
}

// Importer is implemented by every parser.
type Importer interface {
	// ID is the stable string used by the upload-UI dropdown.
	ID() string
	// CanAutoDetect returns true if the parser can recognize that a given
	// (filename, body) belongs to it without explicit selection. Used by
	// the auto-detect path in upload.
	CanAutoDetect(filename string, body []byte) bool
	// Parse runs the importer. body is the raw PNG bytes; configJSON is
	// parser-specific options (e.g. grid dimensions for the raw parser,
	// sidecar JSON for Aseprite).
	Parse(ctx context.Context, body []byte, configJSON []byte) (*ImportResult, error)
}

// Errors returned by importers. Stable for HTTP handler mapping.
var (
	ErrUnknownParser = errors.New("importer: unknown parser id")
	ErrParseFailed   = errors.New("importer: parse failed")
)

// Registry holds the registered importers, keyed by ID.
type Registry struct {
	importers map[string]Importer
}

// NewRegistry returns an empty registry. Tests construct fresh ones.
func NewRegistry() *Registry {
	return &Registry{importers: make(map[string]Importer)}
}

// Register adds an importer. Panics on duplicate id so misconfigurations
// surface at boot time.
func (r *Registry) Register(imp Importer) {
	if _, ok := r.importers[imp.ID()]; ok {
		panic(fmt.Sprintf("importer: duplicate id %q", imp.ID()))
	}
	r.importers[imp.ID()] = imp
}

// Get returns an importer by id.
func (r *Registry) Get(id string) (Importer, bool) {
	imp, ok := r.importers[id]
	return imp, ok
}

// AutoDetect runs CanAutoDetect on every registered importer in stable
// order, returning the first one that claims the file. Returns
// (nil, false) if none match.
func (r *Registry) AutoDetect(filename string, body []byte) (Importer, bool) {
	ids := make([]string, 0, len(r.importers))
	for id := range r.importers {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic
	for _, id := range ids {
		if imp := r.importers[id]; imp.CanAutoDetect(filename, body) {
			return imp, true
		}
	}
	return nil, false
}

// IDs returns every registered importer id, sorted. Used by the UI to
// populate the parser-override dropdown.
func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.importers))
	for id := range r.importers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// DefaultRegistry returns a registry with every built-in parser registered.
// Server boot calls this once and shares the result across requests.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(&RawPNGImporter{})
	r.Register(&StripImporter{})
	r.Register(&AsepriteImporter{})
	r.Register(&TexturePackerImporter{})
	r.Register(&FreeTexPackerImporter{})
	return r
}
