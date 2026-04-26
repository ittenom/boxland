package assets_test

import (
	"testing"

	"boxland/server/internal/assets"
)

func TestWalkAnimForFacing(t *testing.T) {
	cases := []struct {
		f    uint8
		want string
	}{
		{assets.FacingNorth, assets.AnimWalkN},
		{assets.FacingEast, assets.AnimWalkE},
		{assets.FacingSouth, assets.AnimWalkS},
		{assets.FacingWest, assets.AnimWalkW},
		{99, assets.AnimWalkS}, // unknown -> south (camera-facing default)
	}
	for _, c := range cases {
		if got := assets.WalkAnimForFacing(c.f); got != c.want {
			t.Errorf("facing %d: got %q, want %q", c.f, got, c.want)
		}
	}
}

func TestFacingForWalkAnim(t *testing.T) {
	if f, ok := assets.FacingForWalkAnim("walk_north"); !ok || f != assets.FacingNorth {
		t.Errorf("walk_north: got (%d, %v)", f, ok)
	}
	if f, ok := assets.FacingForWalkAnim("WALK_EAST"); !ok || f != assets.FacingEast {
		t.Errorf("case-insensitive walk_east: got (%d, %v)", f, ok)
	}
	if _, ok := assets.FacingForWalkAnim("idle"); ok {
		t.Errorf("idle should not resolve to a facing")
	}
	if _, ok := assets.FacingForWalkAnim("walk"); ok {
		t.Errorf("non-directional walk should not resolve to a facing")
	}
}

func TestPickWalkAnim_FallbackChain(t *testing.T) {
	// 1. exact directional match wins.
	if got, _ := assets.PickWalkAnim([]string{"idle", "walk_east", "walk"}, assets.FacingEast); got != "walk_east" {
		t.Errorf("exact: got %q", got)
	}
	// 2. non-directional walk when directional missing.
	if got, _ := assets.PickWalkAnim([]string{"idle", "walk"}, assets.FacingEast); got != "walk" {
		t.Errorf("walk fallback: got %q", got)
	}
	// 3. idle when no walk at all.
	if got, _ := assets.PickWalkAnim([]string{"idle", "attack"}, assets.FacingEast); got != "idle" {
		t.Errorf("idle fallback: got %q", got)
	}
	// 4. first listed when nothing canonical.
	if got, _ := assets.PickWalkAnim([]string{"attack", "death"}, assets.FacingEast); got != "attack" {
		t.Errorf("first-listed fallback: got %q", got)
	}
	// empty -> false
	if got, ok := assets.PickWalkAnim(nil, assets.FacingEast); ok || got != "" {
		t.Errorf("empty: got (%q, %v)", got, ok)
	}
}

func TestPickIdleAnim_FallbackChain(t *testing.T) {
	if got, _ := assets.PickIdleAnim([]string{"walk_north", "idle", "death"}); got != "idle" {
		t.Errorf("idle exact: got %q", got)
	}
	// no idle -> walk_south (south = camera-facing default for top-down art).
	if got, _ := assets.PickIdleAnim([]string{"walk_south", "walk_east"}); got != "walk_south" {
		t.Errorf("walk_south fallback: got %q", got)
	}
	if got, _ := assets.PickIdleAnim([]string{"walk_east"}); got != "walk_east" {
		t.Errorf("any walk fallback: got %q", got)
	}
	if got, _ := assets.PickIdleAnim([]string{"attack"}); got != "attack" {
		t.Errorf("first-listed: got %q", got)
	}
	if _, ok := assets.PickIdleAnim(nil); ok {
		t.Errorf("empty should return false")
	}
}

func TestFacingFromVelocity(t *testing.T) {
	cases := []struct {
		vx, vy int32
		want   uint8
		hasDir bool
	}{
		{0, 0, 0, false},
		{100, 0, assets.FacingEast, true},
		{-100, 0, assets.FacingWest, true},
		{0, 100, assets.FacingSouth, true},
		{0, -100, assets.FacingNorth, true},
		// Diagonal: horizontal wins on tie.
		{100, 100, assets.FacingEast, true},
		{100, -100, assets.FacingEast, true},
		{-100, 100, assets.FacingWest, true},
		{-50, -100, assets.FacingNorth, true}, // |vy| > |vx|
	}
	for _, c := range cases {
		got, ok := assets.FacingFromVelocity(c.vx, c.vy)
		if ok != c.hasDir {
			t.Errorf("(%d,%d): hasDir got %v, want %v", c.vx, c.vy, ok, c.hasDir)
		}
		if ok && got != c.want {
			t.Errorf("(%d,%d): facing got %d, want %d", c.vx, c.vy, got, c.want)
		}
	}
}
