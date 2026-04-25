package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// TexturePackerImporter handles TexturePacker's JSON-Hash export.
//
// Sidecar shape (from https://www.codeandweb.com/texturepacker):
//
//   {
//     "frames": { "boss-walk-0.png": { "frame": {x,y,w,h}, ... }, ... },
//     "meta": { "app": "https://www.codeandweb.com/texturepacker", "size": {w,h} }
//   }
//
// TexturePacker doesn't ship animation tags out of the box; we infer them
// from filename conventions: a frame named "<name>-<n>.png" or
// "<name>_<n>.png" contributes to animation "<name>". The designer can edit
// the inferred tags in the UI.
type TexturePackerImporter struct{}

func (*TexturePackerImporter) ID() string { return "texturepacker" }

func (*TexturePackerImporter) CanAutoDetect(filename string, body []byte) bool {
	if !strings.HasSuffix(strings.ToLower(filename), ".json") {
		return false
	}
	return bytes.Contains(body, []byte(`codeandweb.com/texturepacker`)) ||
		bytes.Contains(body, []byte(`"app": "TexturePacker"`))
}

type tpFrame struct {
	Frame asepriteRect `json:"frame"` // identical shape to aseprite
}

type tpSidecar struct {
	Frames map[string]tpFrame `json:"frames"`
}

func (p *TexturePackerImporter) Parse(ctx context.Context, body []byte, configJSON []byte) (*ImportResult, error) {
	src := configJSON
	if len(src) == 0 {
		src = body
	}
	var sc tpSidecar
	if err := json.Unmarshal(src, &sc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseFailed, err)
	}
	if len(sc.Frames) == 0 {
		return nil, fmt.Errorf("%w: no frames in TexturePacker sidecar", ErrParseFailed)
	}

	// Stable frame order: sort filenames so the inferred index is consistent
	// across re-imports (necessary for animation tags to land on the right
	// frames when the designer hasn't customized them).
	names := make([]string, 0, len(sc.Frames))
	for n := range sc.Frames {
		names = append(names, n)
	}
	sort.Strings(names)

	frames := make([]FrameRect, 0, len(names))
	for i, name := range names {
		f := sc.Frames[name]
		frames = append(frames, FrameRect{
			Index: i,
			SX:    f.Frame.X, SY: f.Frame.Y,
			SW: f.Frame.W, SH: f.Frame.H,
		})
	}

	anims := inferAnimsFromFilenames(names)

	gridW, gridH := modeFrameSize(frames)
	return &ImportResult{
		Frames:     frames,
		Animations: anims,
		SheetMetadata: SheetMetadata{
			GridW:      gridW,
			GridH:      gridH,
			FrameCount: len(frames),
			Source:     "texturepacker",
		},
	}, nil
}

// inferAnimsFromFilenames groups filenames like "boss-walk-0.png",
// "boss-walk-1.png" into a single animation "boss-walk" spanning frames
// [from..to] (the indices the frames received via stable sort).
func inferAnimsFromFilenames(sortedNames []string) []Animation {
	type span struct{ from, to int }
	groups := make(map[string]*span)
	order := make([]string, 0)

	for i, n := range sortedNames {
		base := stripFrameSuffix(strings.TrimSuffix(strings.TrimSuffix(n, ".png"), ".PNG"))
		if base == "" {
			continue
		}
		s, ok := groups[base]
		if !ok {
			s = &span{from: i, to: i}
			groups[base] = s
			order = append(order, base)
		} else {
			if i > s.to {
				s.to = i
			}
		}
	}

	out := make([]Animation, 0, len(order))
	for _, base := range order {
		s := groups[base]
		// Skip single-frame "anims" -- they're really just static sprites
		// and would clutter the UI.
		if s.to == s.from {
			continue
		}
		out = append(out, Animation{
			Name:      base,
			FrameFrom: s.from,
			FrameTo:   s.to,
			Direction: DirForward,
			FPS:       8,
		})
	}
	return out
}

// stripFrameSuffix removes a trailing "-N", "_N", or " N" from a filename
// so "boss-walk-3" -> "boss-walk". Anything else -> input unchanged.
func stripFrameSuffix(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		c := name[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '-' || c == '_' || c == ' ' {
			if i == 0 {
				return ""
			}
			return name[:i]
		}
		break
	}
	return name
}
