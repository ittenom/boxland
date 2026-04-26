// Boxland — pixel-edge fingerprint extraction for the pixel-WFC engine.
//
// Sources of fingerprints:
//
//   1. ComputeFingerprint(img, frameRect): given a decoded image and the
//      rect of one tile within it (e.g. one 32×32 cell of a sheet),
//      produces an [4]EdgeFingerprint by averaging EdgeSamples evenly-
//      spaced 1-pixel-deep strips along each cardinal edge.
//
//   2. FingerprintCache: in-memory LRU keyed by (entity_type_id, frame,
//      asset_version). Decoder lives outside this package; the cache
//      just stores fingerprints and evicts cheaply.
//
// The cache holds bare arrays — 96 B each — so a project with 4096
// distinct frames fits in well under 1 MiB.
package wfc

import (
	"container/list"
	"errors"
	"image"
	"sync"
)

// ComputeFingerprint samples 4 edges of `frame` inside `img` and returns
// the resulting EdgeFingerprint set. Each sample is the average RGB
// across a 1px-deep strip aligned to the EdgeSamples-evenly-spaced
// columns (or rows). Alpha-zero pixels are skipped so transparent tile
// borders don't pull edges toward (0,0,0); fully-transparent strips fall
// back to the strip's own raw RGB so degenerate tiles still match each
// other.
func ComputeFingerprint(img image.Image, frame image.Rectangle) ([4]EdgeFingerprint, error) {
	if img == nil {
		return [4]EdgeFingerprint{}, errors.New("wfc: ComputeFingerprint nil image")
	}
	bounds := img.Bounds()
	frame = frame.Intersect(bounds)
	if frame.Empty() {
		return [4]EdgeFingerprint{}, errors.New("wfc: ComputeFingerprint empty frame")
	}

	var out [4]EdgeFingerprint
	w := frame.Dx()
	h := frame.Dy()

	// For each edge, pick EdgeSamples evenly-spaced positions along it
	// and average a 1px-deep strip.
	//
	// Edge layout reminder:
	//   N: top row     (y = frame.Min.Y)
	//   E: right col   (x = frame.Max.X-1)
	//   S: bottom row  (y = frame.Max.Y-1)
	//   W: left col    (x = frame.Min.X)
	for sample := 0; sample < EdgeSamples; sample++ {
		// Each sample owns one slice of the edge. width/height >= 1 by
		// the Intersect+Empty check above; div by EdgeSamples is fine.
		colA := frame.Min.X + sample*w/EdgeSamples
		colB := frame.Min.X + (sample+1)*w/EdgeSamples
		if colB <= colA {
			colB = colA + 1
		}
		if colB > frame.Max.X {
			colB = frame.Max.X
		}
		rowA := frame.Min.Y + sample*h/EdgeSamples
		rowB := frame.Min.Y + (sample+1)*h/EdgeSamples
		if rowB <= rowA {
			rowB = rowA + 1
		}
		if rowB > frame.Max.Y {
			rowB = frame.Max.Y
		}

		out[EdgeN][sample] = averageRGB(img, image.Rect(colA, frame.Min.Y, colB, frame.Min.Y+1))
		out[EdgeS][sample] = averageRGB(img, image.Rect(colA, frame.Max.Y-1, colB, frame.Max.Y))
		out[EdgeW][sample] = averageRGB(img, image.Rect(frame.Min.X, rowA, frame.Min.X+1, rowB))
		out[EdgeE][sample] = averageRGB(img, image.Rect(frame.Max.X-1, rowA, frame.Max.X, rowB))
	}
	return out, nil
}

// averageRGB returns the alpha-weighted mean of the colours inside r.
// Transparent pixels (alpha=0) are excluded — they're typically tile
// padding that would otherwise drag every edge toward black.
func averageRGB(img image.Image, r image.Rectangle) [3]uint8 {
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return [3]uint8{}
	}
	var sumR, sumG, sumB, sumA uint64
	var nOpaque uint64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			rc, gc, bc, ac := img.At(x, y).RGBA() // returns 16-bit per channel
			if ac == 0 {
				continue
			}
			sumR += uint64(rc) * uint64(ac)
			sumG += uint64(gc) * uint64(ac)
			sumB += uint64(bc) * uint64(ac)
			sumA += uint64(ac)
			nOpaque++
		}
	}
	if nOpaque == 0 || sumA == 0 {
		// Whole strip is transparent: fall back to a raw average so
		// the tile is still distinguishable from other transparent
		// tiles. RGBA() on a transparent pixel returns 0 anyway, so
		// this collapses to (0,0,0).
		return [3]uint8{}
	}
	return [3]uint8{
		uint8((sumR / sumA) >> 8),
		uint8((sumG / sumA) >> 8),
		uint8((sumB / sumA) >> 8),
	}
}

// CompositeFingerprint averages a set of edge fingerprints into one. Used
// by tile-groups in pixel mode — the group's outer edge fingerprint is the
// average of its boundary cells' edges. Returns the zero fingerprint for
// an empty input (caller should drop such groups).
func CompositeFingerprint(parts []EdgeFingerprint) EdgeFingerprint {
	var out EdgeFingerprint
	if len(parts) == 0 {
		return out
	}
	var sums [EdgeSamples][3]uint64
	for _, p := range parts {
		for s := 0; s < EdgeSamples; s++ {
			for c := 0; c < 3; c++ {
				sums[s][c] += uint64(p[s][c])
			}
		}
	}
	n := uint64(len(parts))
	for s := 0; s < EdgeSamples; s++ {
		for c := 0; c < 3; c++ {
			out[s][c] = uint8(sums[s][c] / n)
		}
	}
	return out
}

// FingerprintKey identifies one cached fingerprint set.
type FingerprintKey struct {
	EntityTypeID int64
	Frame        int32 // atlas index inside the source sheet
	AssetVersion int64 // bumps when the underlying asset is reuploaded
}

// FingerprintCache is an in-memory LRU. Safe for concurrent use; the
// designer realm hits this from many request goroutines.
type FingerprintCache struct {
	cap  int
	mu   sync.Mutex
	ll   *list.List // front = most recently used
	idx  map[FingerprintKey]*list.Element
}

type fpEntry struct {
	key FingerprintKey
	val [4]EdgeFingerprint
}

// NewFingerprintCache constructs a cache with the given capacity. 4096
// entries ≈ 384 KiB; pick a cap that comfortably exceeds your project's
// distinct-tile-frame count.
func NewFingerprintCache(cap int) *FingerprintCache {
	if cap <= 0 {
		cap = 4096
	}
	return &FingerprintCache{
		cap: cap,
		ll:  list.New(),
		idx: make(map[FingerprintKey]*list.Element, cap),
	}
}

// Get returns the cached value and true on hit.
func (c *FingerprintCache) Get(k FingerprintKey) ([4]EdgeFingerprint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.idx[k]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*fpEntry).val, true
	}
	return [4]EdgeFingerprint{}, false
}

// Put inserts (or refreshes) the entry. Evicts the LRU entry on overflow.
func (c *FingerprintCache) Put(k FingerprintKey, v [4]EdgeFingerprint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.idx[k]; ok {
		el.Value.(*fpEntry).val = v
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&fpEntry{key: k, val: v})
	c.idx[k] = el
	for c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.idx, oldest.Value.(*fpEntry).key)
	}
}

// Len returns the current cached entry count. Intended for tests + ops
// dashboards; constant-time.
func (c *FingerprintCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// Drop forces eviction of one key. Used when the asset re-uploads but
// the version-bump propagation is lazy — designers expect "save and
// reload" to reflect immediately.
func (c *FingerprintCache) Drop(k FingerprintKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.idx[k]; ok {
		c.ll.Remove(el)
		delete(c.idx, k)
	}
}

// ColorOK is a small helper so callers building synthetic fingerprints
// in tests don't have to write `[3]uint8{r, g, b}` literals.
func ColorOK(r, g, b uint8) [3]uint8 { return [3]uint8{r, g, b} }
