package maps

import (
	"testing"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps/wfc"
	"boxland/server/internal/sim/collision"
)

func TestRotateEdgeMask(t *testing.T) {
	got := RotateEdgeMask(collision.EdgeN|collision.EdgeE, 90)
	want := collision.EdgeE | collision.EdgeS
	if got != want {
		t.Fatalf("got %04b, want %04b", got, want)
	}
	if got := RotateEdgeMask(collision.EdgeN, 270); got != collision.EdgeW {
		t.Fatalf("N rotated 270: got %04b, want W", got)
	}
}

func TestRotateCollisionShape(t *testing.T) {
	cases := []struct {
		shape collision.CollisionShape
		deg   int16
		want  collision.CollisionShape
	}{
		{collision.ShapeWallNorth, 90, collision.ShapeWallEast},
		{collision.ShapeWallNorth, 180, collision.ShapeWallSouth},
		{collision.ShapeDiagNE, 90, collision.ShapeDiagSE},
		{collision.ShapeHalfWest, 270, collision.ShapeHalfSouth},
		{collision.ShapeOneWayN, 90, collision.ShapeWallEast},
	}
	for _, tc := range cases {
		if got := RotateCollisionShape(tc.shape, tc.deg); got != tc.want {
			t.Fatalf("RotateCollisionShape(%v,%d): got %v want %v", tc.shape, tc.deg, got, tc.want)
		}
	}
}

func TestRotateCollider(t *testing.T) {
	in := components.Collider{W: 10, H: 20, AnchorX: 2, AnchorY: 3, Mask: 7}
	got := RotateCollider(in, 90, 32)
	want := components.Collider{W: 20, H: 10, AnchorX: 29, AnchorY: 2, Mask: 7}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRotateSockets(t *testing.T) {
	in := [4]wfc.SocketID{1, 2, 3, 4}
	got := RotateSockets(in, 90)
	want := [4]wfc.SocketID{4, 1, 2, 3}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
