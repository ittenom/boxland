// Package pathfind implements A* over the tile grid for tap-to-move
// (iOS at v1.1) and "MoveToward X" automation actions.
//
// The walkability check honors each entity's collision-layer mask: a tile
// is walkable iff (tile.collision_layer_mask & entity.mask) == 0 OR if
// the entity-facing edges allow movement *into* the tile from every
// approach direction. For pathfinding we use the more conservative
// "any approach blocks => unwalkable" rule, which is what the typical
// "go from A to B" UX expects.
package pathfind

import (
	"container/heap"
	"errors"

	"boxland/server/internal/sim/collision"
)

// Point is a tile-grid coordinate.
type Point struct {
	X, Y int32
}

// Path is the sequence of tiles from start (inclusive) to goal (inclusive).
type Path []Point

// World is the tile-lookup interface the planner queries. The same
// collision.World interface works because we only need TileAt; no extra
// runtime data is required.
type World = collision.World

// FindPath returns the shortest 4-connected path from start to goal,
// honoring the entity's collision mask. Returns ErrNoPath if none exists
// within `maxNodes` expansions.
//
// `maxNodes` caps the search so a malformed map (or a goal sealed inside
// solid tiles) doesn't run unbounded. 4096 is generous for typical AOI-
// sized destinations on chunk-scale maps.
func FindPath(world World, start, goal Point, mask uint32, maxNodes int) (Path, error) {
	if start == goal {
		return Path{start}, nil
	}
	if maxNodes <= 0 {
		maxNodes = 4096
	}

	open := &openSet{}
	heap.Init(open)

	heap.Push(open, &node{
		pt:    start,
		gScore: 0,
		fScore: heuristic(start, goal),
	})

	gScores := map[Point]int32{start: 0}
	cameFrom := map[Point]Point{}
	expanded := 0

	for open.Len() > 0 {
		current := heap.Pop(open).(*node)
		if current.pt == goal {
			return reconstruct(cameFrom, current.pt), nil
		}
		expanded++
		if expanded > maxNodes {
			return nil, ErrSearchExhausted
		}
		for _, n := range neighbours(current.pt) {
			if !walkable(world, n, mask) {
				continue
			}
			tentative := current.gScore + 1
			if g, ok := gScores[n]; !ok || tentative < g {
				gScores[n] = tentative
				cameFrom[n] = current.pt
				heap.Push(open, &node{
					pt:     n,
					gScore: tentative,
					fScore: tentative + heuristic(n, goal),
				})
			}
		}
	}
	return nil, ErrNoPath
}

// Errors returned by FindPath. Stable for handler mapping.
var (
	ErrNoPath          = errors.New("pathfind: no path between start and goal")
	ErrSearchExhausted = errors.New("pathfind: search budget exceeded")
)

// ---- internals ----

func walkable(world World, p Point, mask uint32) bool {
	t, ok := world.TileAt(p.X, p.Y)
	if !ok {
		// Empty tile: nothing to collide with, freely walkable.
		return true
	}
	if t.CollisionLayerMask&mask == 0 {
		// Tile exists but doesn't share any collision layer with us.
		return true
	}
	// Tile blocks at least one direction; for pathfinding's "is this tile
	// walkable" question we treat ANY edge bit as blocking. A future
	// extension could return per-direction walkability (e.g. one-way
	// ledges in PLAN.md §11 #6); the simple v1 rule is safe.
	return t.EdgeCollisions == 0
}

func neighbours(p Point) [4]Point {
	return [4]Point{
		{X: p.X + 1, Y: p.Y},
		{X: p.X - 1, Y: p.Y},
		{X: p.X, Y: p.Y + 1},
		{X: p.X, Y: p.Y - 1},
	}
}

func heuristic(a, b Point) int32 {
	dx := a.X - b.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - b.Y
	if dy < 0 {
		dy = -dy
	}
	return dx + dy // Manhattan distance; admissible for 4-connected grids
}

func reconstruct(cameFrom map[Point]Point, end Point) Path {
	out := Path{end}
	for {
		prev, ok := cameFrom[end]
		if !ok {
			break
		}
		out = append(out, prev)
		end = prev
	}
	// Reverse in place so the path runs start -> goal.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ---- priority queue ----

type node struct {
	pt     Point
	gScore int32
	fScore int32
	heapIx int
}

type openSet []*node

func (o openSet) Len() int { return len(o) }
func (o openSet) Less(i, j int) bool {
	return o[i].fScore < o[j].fScore
}
func (o openSet) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
	o[i].heapIx = i
	o[j].heapIx = j
}
func (o *openSet) Push(x any) {
	n := x.(*node)
	n.heapIx = len(*o)
	*o = append(*o, n)
}
func (o *openSet) Pop() any {
	old := *o
	n := len(old)
	x := old[n-1]
	x.heapIx = -1
	*o = old[:n-1]
	return x
}
