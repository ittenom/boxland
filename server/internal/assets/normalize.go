// Boxland — image normalization & validation.
//
// Per PLAN.md §4d: "enforce 32x32 cell, validate non-square inputs, error
// UX for bad dimensions". The project ships at a 32-pixel tile/sprite
// scale; uploads at other cell sizes go through but produce structured
// warnings the Asset Manager surfaces in the upload modal.
//
// Per-asset opt-out is supported: bosses, decorative props, and HUD art
// often use non-32 sizes. The Service.Upload path attaches any
// NormalizationWarning to the response so the UI can render it next to
// the saved row.

package assets

import (
	"errors"
	"fmt"
)

// CanonicalCellPx is the project-wide sprite/tile cell size in pixels.
// Mirror of PLAN.md §1 "32px pixel-art aesthetic".
const CanonicalCellPx = 32

// NormalizationProblem categorizes an issue with an imported sheet.
type NormalizationProblem string

const (
	ProblemNone           NormalizationProblem = "none"
	ProblemNonSquareCell  NormalizationProblem = "non_square_cell"
	ProblemNonCanonical   NormalizationProblem = "non_canonical_cell"
	ProblemMixedCellSizes NormalizationProblem = "mixed_cell_sizes"
	ProblemEmptySheet     NormalizationProblem = "empty_sheet"
)

// NormalizationWarning is the structured payload the upload handler surfaces
// to the UI when a sheet imports successfully but with a deviation worth
// flagging. Severity = "warn" means the upload proceeds; "error" means the
// upload was rejected (currently never; reserved for future tightening).
type NormalizationWarning struct {
	Problem  NormalizationProblem `json:"problem"`
	Severity string               `json:"severity"` // "warn" | "error"
	Message  string               `json:"message"`
	GotW     int                  `json:"got_w,omitempty"`
	GotH     int                  `json:"got_h,omitempty"`
	WantW    int                  `json:"want_w,omitempty"`
	WantH    int                  `json:"want_h,omitempty"`
}

// ErrNormalizationFatal is returned for problems that block the upload
// (currently only ProblemEmptySheet). All other problems return as
// non-nil warnings without an error.
var ErrNormalizationFatal = errors.New("normalize: fatal problem")

// Normalize inspects the imported frames and returns either a warning the
// caller should surface, or nil if everything's clean. Returns
// (warning, ErrNormalizationFatal) for unrecoverable issues.
func Normalize(res *ImportResult) (*NormalizationWarning, error) {
	if res == nil || len(res.Frames) == 0 {
		return &NormalizationWarning{
			Problem:  ProblemEmptySheet,
			Severity: "error",
			Message:  "Sheet contains no frames.",
		}, ErrNormalizationFatal
	}

	// Check for non-square cells using the modal frame size from the result.
	w, h := res.SheetMetadata.GridW, res.SheetMetadata.GridH

	// If GridW/GridH weren't filled in (older importers), recompute.
	if w == 0 || h == 0 {
		w, h = modeFrameSize(res.Frames)
	}

	// Mixed sizes within the sheet (some Aseprite exports allow this).
	if mixedCellSizes(res.Frames) {
		return &NormalizationWarning{
			Problem:  ProblemMixedCellSizes,
			Severity: "warn",
			Message:  fmt.Sprintf("Sheet contains frames of varying sizes; modal size %dx%d will be used as the grid.", w, h),
			GotW:     w,
			GotH:     h,
		}, nil
	}

	if w != h {
		return &NormalizationWarning{
			Problem:  ProblemNonSquareCell,
			Severity: "warn",
			Message:  fmt.Sprintf("Cell is %dx%d (not square). Sprites and tiles render best at square sizes.", w, h),
			GotW:     w,
			GotH:     h,
		}, nil
	}

	if w != CanonicalCellPx {
		return &NormalizationWarning{
			Problem:  ProblemNonCanonical,
			Severity: "warn",
			Message:  fmt.Sprintf("Cell is %dx%d, not the project-wide %dx%d. The asset will import; consider re-exporting if it's meant for the standard tile grid.", w, h, CanonicalCellPx, CanonicalCellPx),
			GotW:     w,
			GotH:     h,
			WantW:    CanonicalCellPx,
			WantH:    CanonicalCellPx,
		}, nil
	}

	return nil, nil
}

// mixedCellSizes returns true if not every frame in the sheet has the same
// (sw, sh). Sheets from Aseprite hash exports often do; sheets from raw or
// strip parsers never do.
func mixedCellSizes(frames []FrameRect) bool {
	if len(frames) == 0 {
		return false
	}
	w, h := frames[0].SW, frames[0].SH
	for _, f := range frames[1:] {
		if f.SW != w || f.SH != h {
			return true
		}
	}
	return false
}
