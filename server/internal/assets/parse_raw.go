package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder

	"strings"
)

// RawPNGImporter slices a PNG into uniform grid cells. The simplest parser:
// designer specifies grid_w and grid_h, we count cells across (cols) and
// down (rows) and emit one frame per cell, row-major.
//
// No animation tags are produced -- the designer adds them later in the
// Asset Manager UI. This is the fallback when no metadata sidecar exists.
type RawPNGImporter struct{}

// RawPNGConfig is the JSON payload Parse expects.
type RawPNGConfig struct {
	GridW int `json:"grid_w"`
	GridH int `json:"grid_h"`
}

func (*RawPNGImporter) ID() string { return "raw" }

// CanAutoDetect always returns false: the raw parser is the explicit fallback
// when no other parser claims the file. Auto-detect should never pick it
// silently.
func (*RawPNGImporter) CanAutoDetect(string, []byte) bool { return false }

func (p *RawPNGImporter) Parse(ctx context.Context, body []byte, configJSON []byte) (*ImportResult, error) {
	var cfg RawPNGConfig
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("%w: bad config: %v", ErrParseFailed, err)
		}
	}
	if cfg.GridW <= 0 || cfg.GridH <= 0 {
		return nil, fmt.Errorf("%w: grid_w and grid_h must be > 0", ErrParseFailed)
	}

	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseFailed, err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	if w%cfg.GridW != 0 || h%cfg.GridH != 0 {
		return nil, fmt.Errorf("%w: image %dx%d is not divisible by grid %dx%d",
			ErrParseFailed, w, h, cfg.GridW, cfg.GridH)
	}

	cols := w / cfg.GridW
	rows := h / cfg.GridH
	frames := make([]FrameRect, 0, cols*rows)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			frames = append(frames, FrameRect{
				Index: r*cols + c,
				SX:    c * cfg.GridW,
				SY:    r * cfg.GridH,
				SW:    cfg.GridW,
				SH:    cfg.GridH,
			})
		}
	}

	return &ImportResult{
		Frames: frames,
		// No animations until the designer adds them.
		Animations: nil,
		SheetMetadata: SheetMetadata{
			GridW:      cfg.GridW,
			GridH:      cfg.GridH,
			Cols:       cols,
			Rows:       rows,
			FrameCount: len(frames),
			Source:     "raw",
		},
	}, nil
}

// validatePNG decodes the image header only and returns a clean error on
// malformed PNGs. Used by the shared image-normalization step (task #56).
// Lives here so all PNG-aware code shares the same decoder registration.
func validatePNG(body []byte) (image.Config, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		// Strip wrapping noise from image.Decode error so the message lands
		// nicely in a structured log.
		return image.Config{}, errors.New("invalid PNG: " + strings.TrimPrefix(err.Error(), "image: "))
	}
	return cfg, nil
}
