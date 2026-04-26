package wfc

import (
	"testing"
)

// makeStripeSample builds a 4x4 sample of vertical stripes:
// columns alternate entity types 1 and 2.
func makeStripeSample() SamplePatch {
	tiles := make([]EntityTypeID, 16)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if x%2 == 0 {
				tiles[y*4+x] = 1
			} else {
				tiles[y*4+x] = 2
			}
		}
	}
	return SamplePatch{Width: 4, Height: 4, Tiles: tiles}
}

// makeCheckerSample builds a 4x4 checker of entity types 1 and 2.
func makeCheckerSample() SamplePatch {
	tiles := make([]EntityTypeID, 16)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if (x+y)%2 == 0 {
				tiles[y*4+x] = 1
			} else {
				tiles[y*4+x] = 2
			}
		}
	}
	return SamplePatch{Width: 4, Height: 4, Tiles: tiles}
}

func TestGenerateOverlapping_RejectsEmptySample(t *testing.T) {
	_, err := GenerateOverlapping(OverlappingOptions{
		Sample: SamplePatch{},
		Width:  4, Height: 4, Seed: 1,
	})
	if err != ErrEmptySample {
		t.Fatalf("err = %v, want ErrEmptySample", err)
	}
}

func TestGenerateOverlapping_RejectsSampleTooSmallForN(t *testing.T) {
	_, err := GenerateOverlapping(OverlappingOptions{
		Sample:      SamplePatch{Width: 1, Height: 1, Tiles: []EntityTypeID{1}},
		PatternSize: 2,
		Width:       4, Height: 4, Seed: 1,
	})
	if err != ErrSamplePatternsZero {
		t.Fatalf("err = %v, want ErrSamplePatternsZero", err)
	}
}

func TestGenerateOverlapping_RejectsInvalidDimensions(t *testing.T) {
	_, err := GenerateOverlapping(OverlappingOptions{
		Sample: makeStripeSample(),
		Width:  0, Height: 4, Seed: 1,
	})
	if err != ErrInvalidRegion {
		t.Fatalf("err = %v, want ErrInvalidRegion", err)
	}
}

func TestGenerateOverlapping_FillsAllCells(t *testing.T) {
	res, err := GenerateOverlapping(OverlappingOptions{
		Sample: makeStripeSample(),
		Width:  6, Height: 6, Seed: 42,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Region.Cells) != 36 {
		t.Fatalf("got %d cells, want 36", len(res.Region.Cells))
	}
	for _, c := range res.Region.Cells {
		if c.EntityType == 0 {
			t.Errorf("cell (%d,%d) emitted EntityType 0", c.X, c.Y)
		}
	}
}

func TestGenerateOverlapping_DeterministicSameSeed(t *testing.T) {
	opts := OverlappingOptions{
		Sample: makeStripeSample(),
		Width:  6, Height: 6, Seed: 12345,
	}
	a, err := GenerateOverlapping(opts)
	if err != nil {
		t.Fatalf("Generate a: %v", err)
	}
	b, err := GenerateOverlapping(opts)
	if err != nil {
		t.Fatalf("Generate b: %v", err)
	}
	if len(a.Region.Cells) != len(b.Region.Cells) {
		t.Fatalf("cell count differs: %d vs %d", len(a.Region.Cells), len(b.Region.Cells))
	}
	for i := range a.Region.Cells {
		if a.Region.Cells[i] != b.Region.Cells[i] {
			t.Errorf("cell %d differs: %+v vs %+v", i, a.Region.Cells[i], b.Region.Cells[i])
		}
	}
}

func TestGenerateOverlapping_DifferentSeedsDiffer(t *testing.T) {
	a, _ := GenerateOverlapping(OverlappingOptions{
		Sample: makeStripeSample(), Width: 8, Height: 8, Seed: 1,
	})
	b, _ := GenerateOverlapping(OverlappingOptions{
		Sample: makeStripeSample(), Width: 8, Height: 8, Seed: 9999,
	})
	if a == nil || b == nil {
		t.Skip("a generation failed; skipping diff check")
	}
	differs := false
	for i := range a.Region.Cells {
		if a.Region.Cells[i] != b.Region.Cells[i] {
			differs = true
			break
		}
	}
	if !differs {
		t.Errorf("two seeds produced identical output (sample is too constrained or RNG broken)")
	}
}

func TestGenerateOverlapping_PreservesStripePattern(t *testing.T) {
	// Vertical-stripe sample: every output row should also be alternating
	// 1,2,1,2,... (or 2,1,2,1,... — the model can choose either parity).
	res, err := GenerateOverlapping(OverlappingOptions{
		Sample: makeStripeSample(),
		Width:  6, Height: 4, Seed: 7,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	w := int(res.Region.Width)
	for y := 0; y < int(res.Region.Height); y++ {
		for x := 0; x < w-1; x++ {
			a := res.Region.Cells[y*w+x].EntityType
			b := res.Region.Cells[y*w+x+1].EntityType
			if a == b {
				t.Errorf("adjacent cells in row %d at x=%d both = %d (stripe pattern broken)", y, x, a)
			}
		}
	}
}

func TestGenerateOverlapping_PreservesCheckerPattern(t *testing.T) {
	res, err := GenerateOverlapping(OverlappingOptions{
		Sample: makeCheckerSample(),
		Width:  6, Height: 6, Seed: 7,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	w := int(res.Region.Width)
	// Every horizontal and vertical neighbour must differ (proper checker).
	for y := 0; y < int(res.Region.Height); y++ {
		for x := 0; x < w; x++ {
			et := res.Region.Cells[y*w+x].EntityType
			if x+1 < w {
				if et == res.Region.Cells[y*w+x+1].EntityType {
					t.Errorf("horizontal neighbour match at (%d,%d) — checker broken", x, y)
				}
			}
			if y+1 < int(res.Region.Height) {
				if et == res.Region.Cells[(y+1)*w+x].EntityType {
					t.Errorf("vertical neighbour match at (%d,%d) — checker broken", x, y)
				}
			}
		}
	}
}

func TestGenerateOverlapping_RespectsAnchors(t *testing.T) {
	res, err := GenerateOverlapping(OverlappingOptions{
		Sample: makeCheckerSample(),
		Width:  6, Height: 6, Seed: 1,
		Anchors: Anchors{Cells: []Cell{{X: 0, Y: 0, EntityType: 1}}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := res.Region.Cells[0].EntityType; got != 1 {
		t.Errorf("anchor at (0,0) = %d, want 1", got)
	}
}

func TestGenerateOverlapping_DropsAnchorsForUnknownEntityTypes(t *testing.T) {
	// Entity type 9 never appears in the sample; the anchor should be
	// silently dropped (not crash the engine).
	_, err := GenerateOverlapping(OverlappingOptions{
		Sample: makeStripeSample(),
		Width:  6, Height: 6, Seed: 1,
		Anchors: Anchors{Cells: []Cell{{X: 0, Y: 0, EntityType: 9}}},
	})
	if err != nil {
		t.Fatalf("Generate (with bad anchor): %v", err)
	}
}

func TestGenerateOverlapping_PatternFrequencyMatches(t *testing.T) {
	// In a 6x6 stripe sample where 5 columns are "1" and 1 column is "2",
	// the output should be heavily biased toward 1.
	tiles := make([]EntityTypeID, 36)
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			if x == 3 {
				tiles[y*6+x] = 2
			} else {
				tiles[y*6+x] = 1
			}
		}
	}
	sample := SamplePatch{Width: 6, Height: 6, Tiles: tiles}
	res, err := GenerateOverlapping(OverlappingOptions{
		Sample: sample, Width: 16, Height: 16, Seed: 42,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	count1, count2 := 0, 0
	for _, c := range res.Region.Cells {
		switch c.EntityType {
		case 1:
			count1++
		case 2:
			count2++
		}
	}
	if count1 <= count2 {
		t.Errorf("expected entity 1 (5/6 of sample) to dominate output; got %d vs %d", count1, count2)
	}
}

func TestExtractPatterns_DistinctCount(t *testing.T) {
	// Stripe sample at N=2 produces exactly two distinct horizontal pairs:
	//   [1 2] [1 2]
	//   [1 2] [1 2]    -> pattern A: top-left of even col
	// and
	//   [2 1] [2 1]
	//   [2 1] [2 1]    -> pattern B: top-left of odd col
	ps, err := extractPatterns(makeStripeSample(), 2, false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(ps.patterns) != 2 {
		t.Errorf("got %d patterns, want 2", len(ps.patterns))
	}
}

func TestExtractPatterns_StableHashAcrossRuns(t *testing.T) {
	a, _ := extractPatterns(makeCheckerSample(), 2, false)
	b, _ := extractPatterns(makeCheckerSample(), 2, false)
	if len(a.patterns) != len(b.patterns) {
		t.Fatalf("pattern count differs: %d vs %d", len(a.patterns), len(b.patterns))
	}
	for i := range a.patterns {
		if a.patterns[i].id != b.patterns[i].id {
			t.Errorf("pattern %d id differs across runs: %d vs %d", i, a.patterns[i].id, b.patterns[i].id)
		}
	}
}
