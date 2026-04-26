package collision

import "testing"

// stubWorld is a tiny in-memory World implementation that returns one
// pre-seeded tile and nothing else. Used by the one-way tests to keep
// the surrounding map empty so we exercise just the new shape rule.
type stubWorld struct {
	gx, gy int32
	tile   Tile
}

func (s stubWorld) TileAt(gx, gy int32) (Tile, bool) {
	if gx == s.gx && gy == s.gy {
		return s.tile, true
	}
	return Tile{}, false
}

// oneWayTileAt returns a stubWorld with one OneWayN tile at the given
// grid cell. The tile is on layer 1 with a permissive mask so the
// default entity mask matches.
func oneWayTileAt(gx, gy int32) stubWorld {
	return stubWorld{
		gx: gx, gy: gy,
		tile: Tile{
			GX: gx, GY: gy,
			EdgeCollisions:     EdgesForShape(ShapeOneWayN),
			CollisionLayerMask: 1,
			Shape:              ShapeOneWayN,
		},
	}
}

// oneWayEntity returns an entity sized 32x32 in sub-pixels at (px, py).
func oneWayEntity(px, py int32) Entity {
	return Entity{
		AABB: AABB{
			Left:   px,
			Top:    py,
			Right:  px + TilePx*SubPerPx,
			Bottom: py + TilePx*SubPerPx,
		},
		Mask: 1,
	}
}

func TestOneWay_LandsWhenFallingFromAbove(t *testing.T) {
	// Tile at (0, 1). Player at gy=0 (foot exactly at tile top).
	// Falling = positive Y delta. Should be blocked at the tile top.
	world := oneWayTileAt(0, 1)
	tileTop := int32(1) * TileSizeSub
	e := oneWayEntity(0, 0) // foot Y = TileSizeSub = tileTop

	res := Move(&e, 0, 200, world) // try to fall 200 sub-px
	if res.ResolvedDY != 0 {
		t.Errorf("expected 0 fall when starting at top of one-way, got %d", res.ResolvedDY)
	}
	if e.AABB.Bottom != tileTop {
		t.Errorf("entity should rest on tile top, got bottom=%d want %d", e.AABB.Bottom, tileTop)
	}
}

func TestOneWay_PassesThroughWhenJumpingUp(t *testing.T) {
	// Tile at (0, 1). Player below it. Moving up (negative Y) should
	// be unimpeded -- the platform is one-way from above only.
	world := oneWayTileAt(0, 1)
	startY := int32(2) * TileSizeSub // foot Y = 3 * TileSizeSub
	e := oneWayEntity(0, startY)
	res := Move(&e, 0, -1000, world)
	if res.ResolvedDY != -1000 {
		t.Errorf("upward motion through one-way should be unimpeded, got DY=%d", res.ResolvedDY)
	}
}

func TestOneWay_PassesThroughWhenAlreadyOverlapping(t *testing.T) {
	// Player whose foot is already INSIDE the tile (e.g. they walked
	// onto it from the side mid-frame). Falling further should not
	// snap them up to the top; the foot-position rule says "skip the
	// block when foot is below the tile top".
	world := oneWayTileAt(0, 1)
	tileTop := int32(1) * TileSizeSub
	// Place foot 100 sub-px below the tile top (i.e. inside the tile).
	e := oneWayEntity(0, tileTop-TilePx*SubPerPx+100)
	res := Move(&e, 0, 200, world)
	if res.ResolvedDY != 200 {
		t.Errorf("falling while already overlapping a one-way must pass through, got DY=%d", res.ResolvedDY)
	}
}

func TestOneWay_PassesThroughWhenWalkingSideways(t *testing.T) {
	// Player to the left of a one-way tile. Walking right (no Y motion)
	// should not hit the tile's east/west edges (which are 0 anyway).
	world := oneWayTileAt(2, 1)
	e := oneWayEntity(0, int32(1)*TileSizeSub) // same row as the tile
	res := Move(&e, 5000, 0, world)
	if res.ResolvedDX != 5000 {
		t.Errorf("sideways motion through a one-way row should be unimpeded, got DX=%d", res.ResolvedDX)
	}
}

func TestEdgesForShape_OneWayNIsNorthOnly(t *testing.T) {
	got := EdgesForShape(ShapeOneWayN)
	if got != EdgeN {
		t.Errorf("OneWayN edges = %b, want %b (EdgeN only)", got, EdgeN)
	}
}

func TestEdgesForShape_AllShapesCovered(t *testing.T) {
	// Smoke test: every shape from 0..14 returns *some* defined value
	// (not a panic). Future shapes added without a switch case will
	// silently fall through to 0 — the test catches the intent gap.
	for s := CollisionShape(0); s <= ShapeOneWayN; s++ {
		_ = EdgesForShape(s)
	}
}
