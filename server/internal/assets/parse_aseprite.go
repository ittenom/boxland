package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AsepriteImporter parses an Aseprite JSON sidecar.
//
// Sidecar shape (https://www.aseprite.org/docs/sprite-sheet/):
//
//   { "frames": ... ,
//     "meta": {
//       "frameTags": [{name, from, to, direction}, ...],
//       "size": {w, h}
//     }
//   }
//
// `frames` is either:
//   * an object keyed by sliced frame name (hash flavor), or
//   * an array of {filename, frame:{x,y,w,h}, duration, ...} (array flavor)
//
// We accept either; output is identical.
//
// configJSON for this importer is the sidecar JSON itself, not a wrapper.
// The body PNG is consulted only for sanity checks (sheet size).
type AsepriteImporter struct{}

func (*AsepriteImporter) ID() string { return "aseprite" }

// CanAutoDetect: filename ends in .json (sidecar uploaded directly). Body
// inspection happens at parse time. We DO NOT auto-detect a PNG as
// Aseprite — that path requires the explicit sidecar.
func (*AsepriteImporter) CanAutoDetect(filename string, body []byte) bool {
	if !strings.HasSuffix(strings.ToLower(filename), ".json") {
		return false
	}
	// Quick peek: contains a "frames" and "meta" key.
	return bytes.Contains(body, []byte(`"frames"`)) &&
		bytes.Contains(body, []byte(`"meta"`))
}

type asepriteRect struct {
	X, Y, W, H int
}

type asepriteFrame struct {
	Filename string       `json:"filename"`
	Frame    asepriteRect `json:"frame"`
	Duration int          `json:"duration"` // ms
}

type asepriteFrameTag struct {
	Name      string `json:"name"`
	From      int    `json:"from"`
	To        int    `json:"to"`
	Direction string `json:"direction"`
}

type asepriteMeta struct {
	FrameTags []asepriteFrameTag `json:"frameTags"`
}

// asepriteSidecar covers both array and hash variants by accepting RawMessage
// for `frames` and decoding it once we know which shape we're looking at.
type asepriteSidecar struct {
	Frames json.RawMessage `json:"frames"`
	Meta   asepriteMeta    `json:"meta"`
}

func (p *AsepriteImporter) Parse(ctx context.Context, body []byte, configJSON []byte) (*ImportResult, error) {
	src := configJSON
	if len(src) == 0 {
		src = body
	}
	var sc asepriteSidecar
	if err := json.Unmarshal(src, &sc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseFailed, err)
	}

	frames, err := parseAsepriteFrames(sc.Frames)
	if err != nil {
		return nil, err
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("%w: no frames in sidecar", ErrParseFailed)
	}

	anims := make([]Animation, 0, len(sc.Meta.FrameTags))
	for _, t := range sc.Meta.FrameTags {
		dir := DirForward
		switch strings.ToLower(t.Direction) {
		case "reverse":
			dir = DirReverse
		case "pingpong":
			dir = DirPingpong
		}
		fps := 8
		if t.From >= 0 && t.From < len(frames) && frames[t.From].Index >= 0 {
			// Average duration over the tag's frames -> fps. Aseprite stores
			// per-frame duration in ms in `frames[].duration`; we only kept
			// the rect so we'd have to parse twice to get duration. v1 uses
			// the default 8fps unless future work threads duration through.
			_ = fps
		}
		anims = append(anims, Animation{
			Name:      t.Name,
			FrameFrom: t.From,
			FrameTo:   t.To,
			Direction: dir,
			FPS:       fps,
		})
	}

	// Derive sheet dims from the largest frame extent so renderers know the
	// grid. Aseprite sheets aren't always uniform; we report the modal cell.
	gridW, gridH := modeFrameSize(frames)

	return &ImportResult{
		Frames:     frames,
		Animations: anims,
		SheetMetadata: SheetMetadata{
			GridW:      gridW,
			GridH:      gridH,
			Cols:       0, // non-uniform; runtime uses Frames[].SX/SY directly
			Rows:       0,
			FrameCount: len(frames),
			Source:     "aseprite",
		},
	}, nil
}

// parseAsepriteFrames decodes either the hash or array form.
func parseAsepriteFrames(raw json.RawMessage) ([]FrameRect, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	switch raw[0] {
	case '[':
		var arr []asepriteFrame
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, fmt.Errorf("%w: array frames: %v", ErrParseFailed, err)
		}
		out := make([]FrameRect, len(arr))
		for i, f := range arr {
			out[i] = FrameRect{
				Index: i,
				SX:    f.Frame.X, SY: f.Frame.Y,
				SW: f.Frame.W, SH: f.Frame.H,
			}
		}
		return out, nil
	case '{':
		var m map[string]asepriteFrame
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%w: hash frames: %v", ErrParseFailed, err)
		}
		// Stable order: sort by key for deterministic frame indexing.
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]FrameRect, len(keys))
		for i, k := range keys {
			f := m[k]
			out[i] = FrameRect{
				Index: i,
				SX:    f.Frame.X, SY: f.Frame.Y,
				SW: f.Frame.W, SH: f.Frame.H,
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: frames is neither array nor object", ErrParseFailed)
	}
}

// modeFrameSize returns the most common (w, h) frame size in the sheet.
// Used as the grid metadata when frames are uniform; sheets with mixed
// sizes still get the modal value (best-effort).
func modeFrameSize(frames []FrameRect) (int, int) {
	if len(frames) == 0 {
		return 0, 0
	}
	type wh struct{ W, H int }
	counts := make(map[wh]int, len(frames))
	for _, f := range frames {
		counts[wh{f.SW, f.SH}]++
	}
	best := wh{frames[0].SW, frames[0].SH}
	bestN := 0
	for k, n := range counts {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best.W, best.H
}
