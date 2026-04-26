package assets

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// makePNG builds an in-memory PNG of the given size, then sets a single
// opaque pixel for each (col,row) listed in `solidCells`. Cells not in
// the list stay fully transparent. Drives the slicer tests below.
func makePNG(t *testing.T, w, h int, solidCells [][2]int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for _, cell := range solidCells {
		col, row := cell[0], cell[1]
		// Center pixel of the cell — proves we actually scan the
		// interior, not just the corners.
		x := col*TileSize + TileSize/2
		y := row*TileSize + TileSize/2
		img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestSliceTileSheet_RowMajorIndexing(t *testing.T) {
	// 3 cols x 2 rows. Mark cells (0,0), (2,0), (1,1).
	// Expected non-empty indexes: 0, 2, 4.
	bytesPNG := makePNG(t, TileSize*3, TileSize*2, [][2]int{{0, 0}, {2, 0}, {1, 1}})

	cells, md, err := SliceTileSheet(bytesPNG)
	if err != nil {
		t.Fatalf("SliceTileSheet: %v", err)
	}
	if md.Cols != 3 || md.Rows != 2 || md.TileSize != TileSize {
		t.Errorf("metadata dims: got cols=%d rows=%d size=%d; want 3,2,32", md.Cols, md.Rows, md.TileSize)
	}
	if md.NonEmptyCount != 3 {
		t.Errorf("NonEmptyCount = %d; want 3", md.NonEmptyCount)
	}
	wantIdx := []int{0, 2, 4}
	if len(md.NonEmptyIndex) != len(wantIdx) {
		t.Fatalf("NonEmptyIndex = %v; want %v", md.NonEmptyIndex, wantIdx)
	}
	for i, want := range wantIdx {
		if md.NonEmptyIndex[i] != want {
			t.Errorf("NonEmptyIndex[%d] = %d; want %d", i, md.NonEmptyIndex[i], want)
		}
	}
	// Spot-check a couple of cells line up: (col=2, row=0) is index 2;
	// (col=1, row=1) is index 4.
	if cells[2].Col != 2 || cells[2].Row != 0 || !cells[2].NonEmpty {
		t.Errorf("cell[2] = %+v; want col=2 row=0 nonEmpty=true", cells[2])
	}
	if cells[4].Col != 1 || cells[4].Row != 1 || !cells[4].NonEmpty {
		t.Errorf("cell[4] = %+v; want col=1 row=1 nonEmpty=true", cells[4])
	}
	// And an empty one — (col=1, row=0) is index 1, never set.
	if cells[1].NonEmpty {
		t.Errorf("cell[1] should be empty, got %+v", cells[1])
	}
}

func TestSliceTileSheet_RejectsOddDimensions(t *testing.T) {
	bytesPNG := makePNG(t, 50, 32, nil) // 50 isn't divisible by 32
	_, _, err := SliceTileSheet(bytesPNG)
	if !errors.Is(err, ErrNonGridDimensions) {
		t.Fatalf("err = %v; want ErrNonGridDimensions", err)
	}
}

func TestSliceTileSheet_RejectsAllTransparent(t *testing.T) {
	bytesPNG := makePNG(t, TileSize*2, TileSize, nil)
	_, _, err := SliceTileSheet(bytesPNG)
	if !errors.Is(err, ErrEmptyTileSheet) {
		t.Fatalf("err = %v; want ErrEmptyTileSheet", err)
	}
}

func TestSliceTileSheet_RejectsNonPNG(t *testing.T) {
	_, _, err := SliceTileSheet([]byte("hello"))
	if !errors.Is(err, ErrNonPNG) {
		t.Fatalf("err = %v; want ErrNonPNG", err)
	}
}

func TestTileSheetMetadata_RoundTrip(t *testing.T) {
	in := TileSheetMetadata{TileSize: TileSize, Cols: 4, Rows: 2, NonEmptyCount: 2, NonEmptyIndex: []int{0, 7}}
	raw, err := MarshalTileSheetMetadata(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := DecodeTileSheetMetadata(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Cols != in.Cols || got.Rows != in.Rows || got.NonEmptyCount != in.NonEmptyCount {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, in)
	}
}

func TestDecodeTileSheetMetadata_EmptyInputs(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("{}"), []byte("null"), {}} {
		got, err := DecodeTileSheetMetadata(raw)
		if err != nil {
			t.Errorf("DecodeTileSheetMetadata(%q) err = %v", string(raw), err)
		}
		if got.TileSize != 0 || got.Cols != 0 {
			t.Errorf("DecodeTileSheetMetadata(%q) = %+v; want zero", string(raw), got)
		}
	}
}
