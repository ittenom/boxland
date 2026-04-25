package assets_test

import (
	"errors"
	"testing"

	"boxland/server/internal/assets"
)

func uniformResult(w, h, n int) *assets.ImportResult {
	frames := make([]assets.FrameRect, n)
	for i := range frames {
		frames[i] = assets.FrameRect{Index: i, SX: i * w, SY: 0, SW: w, SH: h}
	}
	return &assets.ImportResult{
		Frames: frames,
		SheetMetadata: assets.SheetMetadata{
			GridW: w, GridH: h, FrameCount: n, Source: "test",
		},
	}
}

func TestNormalize_CanonicalIsClean(t *testing.T) {
	w, err := assets.Normalize(uniformResult(32, 32, 4))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if w != nil {
		t.Errorf("expected nil warning, got %+v", w)
	}
}

func TestNormalize_NonSquareWarns(t *testing.T) {
	w, err := assets.Normalize(uniformResult(32, 16, 4))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if w == nil || w.Problem != assets.ProblemNonSquareCell {
		t.Errorf("expected non-square warning, got %+v", w)
	}
	if w.Severity != "warn" {
		t.Errorf("severity should be warn, got %q", w.Severity)
	}
}

func TestNormalize_NonCanonicalWarns(t *testing.T) {
	w, err := assets.Normalize(uniformResult(16, 16, 4))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if w == nil || w.Problem != assets.ProblemNonCanonical {
		t.Errorf("expected non-canonical warning, got %+v", w)
	}
	if w.GotW != 16 || w.WantW != assets.CanonicalCellPx {
		t.Errorf("warning fields: got=%dx%d want=%dx%d",
			w.GotW, w.GotH, w.WantW, w.WantH)
	}
}

func TestNormalize_MixedSizesDetected(t *testing.T) {
	res := &assets.ImportResult{
		Frames: []assets.FrameRect{
			{Index: 0, SW: 32, SH: 32},
			{Index: 1, SW: 64, SH: 64},
			{Index: 2, SW: 32, SH: 32},
		},
		SheetMetadata: assets.SheetMetadata{GridW: 32, GridH: 32, FrameCount: 3, Source: "test"},
	}
	w, err := assets.Normalize(res)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if w == nil || w.Problem != assets.ProblemMixedCellSizes {
		t.Errorf("expected mixed-sizes warning, got %+v", w)
	}
}

func TestNormalize_EmptySheetIsFatal(t *testing.T) {
	w, err := assets.Normalize(&assets.ImportResult{Frames: nil})
	if !errors.Is(err, assets.ErrNormalizationFatal) {
		t.Errorf("expected ErrNormalizationFatal, got %v", err)
	}
	if w == nil || w.Severity != "error" {
		t.Errorf("expected error-severity warning, got %+v", w)
	}
}

func TestNormalize_NilResultIsFatal(t *testing.T) {
	_, err := assets.Normalize(nil)
	if !errors.Is(err, assets.ErrNormalizationFatal) {
		t.Errorf("got %v", err)
	}
}
