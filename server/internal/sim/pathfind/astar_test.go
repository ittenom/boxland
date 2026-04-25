package pathfind_test

import (
	"errors"
	"testing"

	"boxland/server/internal/sim/collision"
	"boxland/server/internal/sim/pathfind"
)

// emptyWorld returns a tile world with no tiles (every cell is open).
func emptyWorld() *collision.MapWorld {
	return collision.BuildWorld(nil)
}

// wallSquareWorld returns a world with a long horizontal wall at y=2
// spanning x ∈ [-20, 20] with a single gap at x=2. The wall is long
// enough that A* can't trivially route around the ends, so the only
// path between y < 2 and y > 2 must thread the gap.
func wallSquareWorld() *collision.MapWorld {
	tiles := []collision.Tile{}
	const wallY int32 = 2
	for x := int32(-20); x <= 20; x++ {
		if x == 2 {
			continue // gap for the path to thread
		}
		tiles = append(tiles, collision.Tile{
			GX: x, GY: wallY,
			EdgeCollisions:     collision.EdgeN | collision.EdgeE | collision.EdgeS | collision.EdgeW,
			CollisionLayerMask: 1,
		})
	}
	return collision.BuildWorld(tiles)
}

func TestFindPath_StartEqualsGoal(t *testing.T) {
	p, err := pathfind.FindPath(emptyWorld(), pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 0, Y: 0}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 1 || p[0] != (pathfind.Point{X: 0, Y: 0}) {
		t.Errorf("trivial path: got %v", p)
	}
}

func TestFindPath_StraightLineOnEmptyMap(t *testing.T) {
	p, err := pathfind.FindPath(emptyWorld(), pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 0, Y: 5}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 6 {
		t.Errorf("len: got %d, want 6", len(p))
	}
	if p[0] != (pathfind.Point{X: 0, Y: 0}) || p[5] != (pathfind.Point{X: 0, Y: 5}) {
		t.Errorf("endpoints wrong: %v", p)
	}
	// Every step is exactly 1 cell apart (4-connected).
	for i := 1; i < len(p); i++ {
		dx := p[i].X - p[i-1].X
		dy := p[i].Y - p[i-1].Y
		if (dx*dx + dy*dy) != 1 {
			t.Errorf("non-cardinal step at %d: %v -> %v", i, p[i-1], p[i])
		}
	}
}

func TestFindPath_ThreadsThroughGap(t *testing.T) {
	w := wallSquareWorld()
	p, err := pathfind.FindPath(w, pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 0, Y: 4}, 1, 0)
	if err != nil {
		t.Fatalf("FindPath: %v", err)
	}
	// The path must pass through the (2, 2) gap.
	hitGap := false
	for _, pt := range p {
		if pt.X == 2 && pt.Y == 2 {
			hitGap = true
		}
	}
	if !hitGap {
		t.Errorf("expected path to thread through (2,2) gap; got %v", p)
	}
}

func TestFindPath_NoPathReturnsErrNoPath(t *testing.T) {
	// Single tile boxing in (0,0) on every side.
	tiles := []collision.Tile{
		{GX: 1, GY: 0, EdgeCollisions: collision.EdgeW, CollisionLayerMask: 1},
		{GX: -1, GY: 0, EdgeCollisions: collision.EdgeE, CollisionLayerMask: 1},
		{GX: 0, GY: 1, EdgeCollisions: collision.EdgeN, CollisionLayerMask: 1},
		{GX: 0, GY: -1, EdgeCollisions: collision.EdgeS, CollisionLayerMask: 1},
	}
	w := collision.BuildWorld(tiles)
	_, err := pathfind.FindPath(w, pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 5, Y: 5}, 1, 0)
	if !errors.Is(err, pathfind.ErrNoPath) {
		t.Errorf("got %v, want ErrNoPath", err)
	}
}

func TestFindPath_HonorsCollisionMask(t *testing.T) {
	// Wall on layer 2 spanning every y the search budget can reach. A
	// layer-1 entity walks through (mask doesn't intersect); a layer-2
	// entity cannot route around (limited search budget).
	tiles := []collision.Tile{}
	for y := int32(-50); y <= 50; y++ {
		tiles = append(tiles, collision.Tile{
			GX: 1, GY: y,
			EdgeCollisions:     collision.EdgeW | collision.EdgeE | collision.EdgeN | collision.EdgeS,
			CollisionLayerMask: 2,
		})
	}
	w := collision.BuildWorld(tiles)

	// Entity on layer 1 should walk straight through (4 cells).
	p, err := pathfind.FindPath(w, pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 3, Y: 0}, 1, 0)
	if err != nil {
		t.Fatalf("layer-1 entity blocked unexpectedly: %v", err)
	}
	if len(p) != 4 {
		t.Errorf("unobstructed path expected len 4, got %d", len(p))
	}

	// Entity on layer 2 cannot find a path within the small budget --
	// either ErrNoPath or ErrSearchExhausted is acceptable evidence the
	// wall is doing its job.
	_, err = pathfind.FindPath(w, pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 3, Y: 0}, 2, 200)
	if err == nil {
		t.Errorf("layer-2 entity should be blocked or budget-exhausted; got nil error")
	}
	if !errors.Is(err, pathfind.ErrNoPath) && !errors.Is(err, pathfind.ErrSearchExhausted) {
		t.Errorf("layer-2 unexpected error: %v", err)
	}
}

func TestFindPath_ExceedingBudgetReturnsErrSearchExhausted(t *testing.T) {
	_, err := pathfind.FindPath(emptyWorld(), pathfind.Point{X: 0, Y: 0}, pathfind.Point{X: 1000, Y: 1000}, 1, 5)
	if !errors.Is(err, pathfind.ErrSearchExhausted) {
		t.Errorf("got %v, want ErrSearchExhausted", err)
	}
}

func TestFindPath_PathStartsAtStart(t *testing.T) {
	p, err := pathfind.FindPath(emptyWorld(), pathfind.Point{X: 3, Y: -2}, pathfind.Point{X: 6, Y: 1}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p[0] != (pathfind.Point{X: 3, Y: -2}) {
		t.Errorf("path[0] should be start; got %v", p[0])
	}
}
