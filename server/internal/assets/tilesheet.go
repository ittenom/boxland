package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
)

// TileSize is the canonical Boxland tile/sprite cell, in pixels.
// Both axes of every uploaded sprite/tile PNG must be a multiple of
// this; the slicer rejects anything else with ErrNonGridDimensions.
const TileSize = 32

// TileSheetMetadata is the JSON shape persisted into assets.metadata_json
// for kind=tile uploads. The mapmaker palette + asset-detail tile grid
// read it without re-decoding the PNG.
type TileSheetMetadata struct {
	TileSize       int   `json:"tile_size"`
	Cols           int   `json:"cols"`
	Rows           int   `json:"rows"`
	NonEmptyCount  int   `json:"non_empty_count"`
	NonEmptyIndex  []int `json:"non_empty_index"` // atlas indexes whose cells contain at least one opaque pixel
}

// TileCell describes one 32x32 region of a tile sheet.
type TileCell struct {
	Index    int  // row-major atlas index (col + row*cols)
	Col      int  // 0-based column
	Row      int  // 0-based row
	NonEmpty bool // false when every pixel in the cell is fully transparent
}

// Errors surfaced by SliceTileSheet. Stable for HTTP handler mapping.
var (
	ErrNonPNG              = errors.New("tilesheet: not a PNG")
	ErrNonGridDimensions   = errors.New("tilesheet: dimensions must be a multiple of 32px on both axes")
	ErrEmptyTileSheet      = errors.New("tilesheet: every cell is fully transparent")
)

// SliceTileSheet decodes a PNG and returns the per-cell layout. It does
// not modify the image — the original bytes still ship to object storage
// unchanged, so the runtime renders directly from the source PNG by
// drawing sub-rects keyed by atlas index.
//
// Cells are reported in row-major order: index 0 is top-left, index
// (cols-1) is top-right of row 0, index cols is the start of row 1.
// This matches the MDN tile-atlas convention (and the Tilemaps article
// the design team links to).
func SliceTileSheet(pngBytes []byte) ([]TileCell, TileSheetMetadata, error) {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, TileSheetMetadata{}, fmt.Errorf("%w: %v", ErrNonPNG, err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 || w%TileSize != 0 || h%TileSize != 0 {
		return nil, TileSheetMetadata{}, fmt.Errorf("%w: got %dx%d", ErrNonGridDimensions, w, h)
	}
	cols, rows := w/TileSize, h/TileSize

	cells := make([]TileCell, 0, cols*rows)
	nonEmpty := make([]int, 0, cols*rows)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			ne := cellHasOpaquePixel(img, b.Min.X+c*TileSize, b.Min.Y+r*TileSize)
			cells = append(cells, TileCell{Index: idx, Col: c, Row: r, NonEmpty: ne})
			if ne {
				nonEmpty = append(nonEmpty, idx)
			}
		}
	}
	if len(nonEmpty) == 0 {
		return nil, TileSheetMetadata{}, ErrEmptyTileSheet
	}
	md := TileSheetMetadata{
		TileSize:      TileSize,
		Cols:          cols,
		Rows:          rows,
		NonEmptyCount: len(nonEmpty),
		NonEmptyIndex: nonEmpty,
	}
	return cells, md, nil
}

// cellHasOpaquePixel returns true if any pixel in the 32x32 region
// starting at (x0,y0) has alpha > 0. We treat alpha-0 as "empty"
// regardless of the RGB channels — RGBA(255,255,255,0) is still an
// erased cell from the artist's perspective.
//
// Uses image.RGBA fast-path when available; falls back to img.At()
// for paletted/grayscale PNGs.
func cellHasOpaquePixel(img image.Image, x0, y0 int) bool {
	x1, y1 := x0+TileSize, y0+TileSize
	if rgba, ok := img.(*image.RGBA); ok {
		stride := rgba.Stride
		base := rgba.PixOffset(x0, y0)
		// Walk row by row reading the alpha byte (offset 3 in RGBA).
		for y := 0; y < TileSize; y++ {
			row := base + y*stride
			for x := 0; x < TileSize; x++ {
				if rgba.Pix[row+x*4+3] != 0 {
					return true
				}
			}
		}
		return false
	}
	if nrgba, ok := img.(*image.NRGBA); ok {
		stride := nrgba.Stride
		base := nrgba.PixOffset(x0, y0)
		for y := 0; y < TileSize; y++ {
			row := base + y*stride
			for x := 0; x < TileSize; x++ {
				if nrgba.Pix[row+x*4+3] != 0 {
					return true
				}
			}
		}
		return false
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a != 0 {
				return true
			}
		}
	}
	return false
}

// MarshalTileSheetMetadata is a convenience for the upload pipeline
// (which writes the metadata JSON straight into assets.metadata_json).
func MarshalTileSheetMetadata(md TileSheetMetadata) ([]byte, error) {
	return json.Marshal(md)
}

// DecodeTileSheetMetadata reads back what MarshalTileSheetMetadata
// wrote. Returns a zero-value (no error) when the metadata column is
// empty or doesn't carry tile-sheet fields, so callers can branch on
// `md.TileSize == 0` to mean "unknown / single-frame sprite".
func DecodeTileSheetMetadata(raw []byte) (TileSheetMetadata, error) {
	var md TileSheetMetadata
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return md, nil
	}
	if err := json.Unmarshal(raw, &md); err != nil {
		return TileSheetMetadata{}, err
	}
	return md, nil
}
