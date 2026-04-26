package assets

import "strings"

// Walk-animation naming convention. These names are how the importer,
// the runtime animation-picker system, and the client renderer agree on
// "the walk-east clip" without trading wire-format ids — names are
// stable across re-imports while ids churn.
//
// Walks are the most-used animation by a wide margin (every player and
// most NPCs walk; few have a unique idle, fewer have specialized
// reactions), so the lookup helpers below are biased toward making them
// fast and forgiving: an asset that lacks a `walk_north` row falls back
// to `walk` then `idle` then frame 0, rather than rendering nothing.
const (
	AnimIdle  = "idle"
	AnimWalk  = "walk" // single non-directional walk; common for top-down
	AnimWalkN = "walk_north"
	AnimWalkE = "walk_east"
	AnimWalkS = "walk_south"
	AnimWalkW = "walk_west"
)

// Facing values mirror the EntityState.facing wire encoding (schemas/world.fbs):
//   0 = N, 1 = E, 2 = S, 3 = W
const (
	FacingNorth uint8 = 0
	FacingEast  uint8 = 1
	FacingSouth uint8 = 2
	FacingWest  uint8 = 3
)

// WalkAnimForFacing returns the canonical walk-animation name for a
// facing direction. Defaults to walk_south on unknown facings (the
// "looking at the camera" pose, which is the conventional default in
// top-down RPG art).
func WalkAnimForFacing(facing uint8) string {
	switch facing {
	case FacingNorth:
		return AnimWalkN
	case FacingEast:
		return AnimWalkE
	case FacingWest:
		return AnimWalkW
	default:
		return AnimWalkS
	}
}

// FacingForWalkAnim is the inverse of WalkAnimForFacing. Returns
// (facing, true) for any of the four directional walk names; false
// otherwise (including for the non-directional `walk` and for `idle`).
func FacingForWalkAnim(name string) (uint8, bool) {
	switch strings.ToLower(name) {
	case AnimWalkN:
		return FacingNorth, true
	case AnimWalkE:
		return FacingEast, true
	case AnimWalkS:
		return FacingSouth, true
	case AnimWalkW:
		return FacingWest, true
	default:
		return 0, false
	}
}

// PickWalkAnim returns the best walk-clip name available among `names`
// for the requested facing. Fallback chain (in order):
//
//   1. exact directional walk (`walk_<facing>`)
//   2. non-directional `walk`
//   3. `idle` (so a stationary-art-only sheet still renders something)
//   4. the first name available
//
// Returns ("", false) only when `names` is empty.
//
// This is the lookup the server-side movement→anim system uses, and
// also what the client falls back to if the per-asset table doesn't
// have the exact clip the server asked for. Same routine in both
// places means the choice is consistent.
func PickWalkAnim(names []string, facing uint8) (string, bool) {
	if len(names) == 0 {
		return "", false
	}
	want := WalkAnimForFacing(facing)
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[strings.ToLower(n)] = struct{}{}
	}
	if _, ok := set[want]; ok {
		return want, true
	}
	if _, ok := set[AnimWalk]; ok {
		return AnimWalk, true
	}
	if _, ok := set[AnimIdle]; ok {
		return AnimIdle, true
	}
	return names[0], true
}

// PickIdleAnim returns the best idle-clip name available. Falls back to
// the first directional walk (rendered at its first frame) if no
// dedicated idle exists — better than nothing, and matches what most
// designers expect from a sheet without a separate idle pose.
func PickIdleAnim(names []string) (string, bool) {
	if len(names) == 0 {
		return "", false
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[strings.ToLower(n)] = struct{}{}
	}
	if _, ok := set[AnimIdle]; ok {
		return AnimIdle, true
	}
	for _, candidate := range []string{AnimWalkS, AnimWalk, AnimWalkN, AnimWalkE, AnimWalkW} {
		if _, ok := set[candidate]; ok {
			return candidate, true
		}
	}
	return names[0], true
}

// FacingFromVelocity picks a facing from a velocity vector. Horizontal
// movement wins on a tie because most pixel-art character sheets treat
// E/W as the dominant facings (the silhouette reads clearer profile-on
// than from in front), so a player holding diagonal NE on the keyboard
// gets walk_east rather than walk_north — which feels right.
//
// Returns (facing, true) when |v| > 0; false on a zero vector so the
// caller can keep the previous facing rather than snapping to a default.
func FacingFromVelocity(vx, vy int32) (uint8, bool) {
	ax := vx
	if ax < 0 {
		ax = -ax
	}
	ay := vy
	if ay < 0 {
		ay = -ay
	}
	if ax == 0 && ay == 0 {
		return 0, false
	}
	if ax >= ay {
		if vx >= 0 {
			return FacingEast, true
		}
		return FacingWest, true
	}
	if vy >= 0 {
		return FacingSouth, true
	}
	return FacingNorth, true
}
