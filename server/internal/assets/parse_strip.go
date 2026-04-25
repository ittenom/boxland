package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
)

// StripImporter handles common strip layouts:
//   * horizontal strip (1 row of N frames)
//   * vertical strip   (1 column of N frames)
//   * "rows N"         (image divided into N equal rows)
//   * "cols N"         (image divided into N equal columns)
//
// The designer picks the layout in the upload UI. Auto-detect picks
// "horizontal" when the image's aspect ratio is wider than tall by a clean
// integer multiple AND the height matches the configured tile size. We
// don't auto-detect aggressively here — designer override is the norm.
type StripImporter struct{}

type StripLayout string

const (
	StripHorizontal StripLayout = "horizontal" // 1 row × N cols
	StripVertical   StripLayout = "vertical"   // N rows × 1 col
	StripRowsN      StripLayout = "rows"       // N rows × auto cols
	StripColsN      StripLayout = "cols"       // auto rows × N cols
)

// StripConfig configures a strip parse.
type StripConfig struct {
	Layout StripLayout `json:"layout"`
	// For horizontal/vertical: cell size in pixels. The "long" axis cell
	// count is auto-derived from the image dimension.
	CellW int `json:"cell_w"`
	CellH int `json:"cell_h"`
	// For rows/cols: how many rows or cols.
	N int `json:"n"`
}

func (*StripImporter) ID() string { return "strip" }

func (*StripImporter) CanAutoDetect(string, []byte) bool { return false }

func (p *StripImporter) Parse(ctx context.Context, body []byte, configJSON []byte) (*ImportResult, error) {
	var cfg StripConfig
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, fmt.Errorf("%w: bad config: %v", ErrParseFailed, err)
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseFailed, err)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()

	cols, rows, cellW, cellH, err := stripDims(cfg, w, h)
	if err != nil {
		return nil, err
	}

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
			Source:     "strip:" + string(cfg.Layout),
		},
	}, nil
}

// stripDims resolves (cols, rows, cellW, cellH) from a config. Returns a
// clean error rather than partial data if dimensions don't divide evenly.
func stripDims(cfg StripConfig, w, h int) (cols, rows, cellW, cellH int, err error) {
	switch cfg.Layout {
	case StripHorizontal:
		if cfg.CellW <= 0 || cfg.CellH <= 0 {
			return 0, 0, 0, 0, fmt.Errorf("%w: horizontal strip requires cell_w and cell_h", ErrParseFailed)
		}
		if w%cfg.CellW != 0 || h != cfg.CellH {
			return 0, 0, 0, 0, fmt.Errorf("%w: image %dx%d does not match horizontal strip cells %dx%d",
				ErrParseFailed, w, h, cfg.CellW, cfg.CellH)
		}
		return w / cfg.CellW, 1, cfg.CellW, cfg.CellH, nil
	case StripVertical:
		if cfg.CellW <= 0 || cfg.CellH <= 0 {
			return 0, 0, 0, 0, fmt.Errorf("%w: vertical strip requires cell_w and cell_h", ErrParseFailed)
		}
		if h%cfg.CellH != 0 || w != cfg.CellW {
			return 0, 0, 0, 0, fmt.Errorf("%w: image %dx%d does not match vertical strip cells %dx%d",
				ErrParseFailed, w, h, cfg.CellW, cfg.CellH)
		}
		return 1, h / cfg.CellH, cfg.CellW, cfg.CellH, nil
	case StripRowsN:
		if cfg.N <= 0 || h%cfg.N != 0 {
			return 0, 0, 0, 0, fmt.Errorf("%w: rows=%d not compatible with image height %d", ErrParseFailed, cfg.N, h)
		}
		ch := h / cfg.N
		// cols comes from cell width = image height / N (square assumption);
		// this matches the convention designers expect for "row strip" sheets.
		if cfg.CellW <= 0 {
			cfg.CellW = ch
		}
		if w%cfg.CellW != 0 {
			return 0, 0, 0, 0, fmt.Errorf("%w: image width %d not divisible by cell_w %d", ErrParseFailed, w, cfg.CellW)
		}
		return w / cfg.CellW, cfg.N, cfg.CellW, ch, nil
	case StripColsN:
		if cfg.N <= 0 || w%cfg.N != 0 {
			return 0, 0, 0, 0, fmt.Errorf("%w: cols=%d not compatible with image width %d", ErrParseFailed, cfg.N, w)
		}
		cw := w / cfg.N
		if cfg.CellH <= 0 {
			cfg.CellH = cw
		}
		if h%cfg.CellH != 0 {
			return 0, 0, 0, 0, fmt.Errorf("%w: image height %d not divisible by cell_h %d", ErrParseFailed, h, cfg.CellH)
		}
		return cfg.N, h / cfg.CellH, cw, cfg.CellH, nil
	default:
		return 0, 0, 0, 0, fmt.Errorf("%w: unknown strip layout %q", ErrParseFailed, cfg.Layout)
	}
}
