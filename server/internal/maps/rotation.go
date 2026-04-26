package maps

import (
	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps/wfc"
	"boxland/server/internal/sim/collision"
)

const TilePixelSize int32 = 32

func rotationSteps(deg int16) int {
	switch deg {
	case 90:
		return 1
	case 180:
		return 2
	case 270:
		return 3
	default:
		return 0
	}
}

func RotateEdgeMask(mask uint8, deg int16) uint8 {
	out := mask
	for i := 0; i < rotationSteps(deg); i++ {
		var next uint8
		if out&collision.EdgeN != 0 {
			next |= collision.EdgeE
		}
		if out&collision.EdgeE != 0 {
			next |= collision.EdgeS
		}
		if out&collision.EdgeS != 0 {
			next |= collision.EdgeW
		}
		if out&collision.EdgeW != 0 {
			next |= collision.EdgeN
		}
		out = next
	}
	return out
}

func RotateCollisionShape(shape collision.CollisionShape, deg int16) collision.CollisionShape {
	out := shape
	for i := 0; i < rotationSteps(deg); i++ {
		switch out {
		case collision.ShapeWallNorth:
			out = collision.ShapeWallEast
		case collision.ShapeWallEast:
			out = collision.ShapeWallSouth
		case collision.ShapeWallSouth:
			out = collision.ShapeWallWest
		case collision.ShapeWallWest:
			out = collision.ShapeWallNorth
		case collision.ShapeDiagNE:
			out = collision.ShapeDiagSE
		case collision.ShapeDiagSE:
			out = collision.ShapeDiagSW
		case collision.ShapeDiagSW:
			out = collision.ShapeDiagNW
		case collision.ShapeDiagNW:
			out = collision.ShapeDiagNE
		case collision.ShapeHalfNorth:
			out = collision.ShapeHalfEast
		case collision.ShapeHalfEast:
			out = collision.ShapeHalfSouth
		case collision.ShapeHalfSouth:
			out = collision.ShapeHalfWest
		case collision.ShapeHalfWest:
			out = collision.ShapeHalfNorth
		case collision.ShapeOneWayN:
			// Current enum only has a north-facing one-way. Rotate to the
			// equivalent single blocking wall edge so collision edges still
			// follow placement orientation until directional one-way enums land.
			out = collision.ShapeWallEast
		default:
			// Open and Solid are rotationally invariant.
		}
	}
	return out
}

func RotateCollider(c components.Collider, deg int16, tilePx int32) components.Collider {
	out := c
	if tilePx <= 0 {
		tilePx = TilePixelSize
	}
	for i := 0; i < rotationSteps(deg); i++ {
		old := out
		out.W = old.H
		out.H = old.W
		out.AnchorX = uint16(clampI32(tilePx-int32(old.AnchorY), 0, tilePx))
		out.AnchorY = old.AnchorX
		out.Mask = old.Mask
	}
	return out
}

func RotateSockets(s [4]wfc.SocketID, deg int16) [4]wfc.SocketID {
	out := s
	for i := 0; i < rotationSteps(deg); i++ {
		out = [4]wfc.SocketID{out[wfc.EdgeW], out[wfc.EdgeN], out[wfc.EdgeE], out[wfc.EdgeS]}
	}
	return out
}

func clampI32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
