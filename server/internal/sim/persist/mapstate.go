// Package persist owns the (de)serialization of game state for both the
// Postgres warm-state blob and the Redis Streams WAL. One MapState
// FlatBuffers schema serves both surfaces (PLAN.md §1 "State serialization").
package persist

import (
	"errors"
	"fmt"

	flatbuffers "github.com/google/flatbuffers/go"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/proto"
	"boxland/server/internal/sim/ecs"
)

// ProtocolVersion mirrors the version we encode/decode. Bumped whenever
// the on-wire MapState shape changes incompatibly. Server refuses to
// decode mismatched majors.
const (
	ProtocolMajor uint16 = 1
	ProtocolMinor uint16 = 0
)

// EncodedMapState is just a labelled []byte; using a named type so callers
// don't accidentally conflate a MapState blob with a Mutation blob etc.
type EncodedMapState []byte

// EncodeInputs bundles the data the encoder consumes. The level-instance
// owner builds this at flush time.
type EncodeInputs struct {
	LevelID    uint32
	InstanceID string
	Tick       uint64
	Stores     *ecs.Stores // entity component data
	// Future: Tiles + Lighting once their components carry the full state.
}

// EncodeMapState serializes the inputs into a MapState FlatBuffer.
//
// Encoding policy for v1:
//   * Every entity owning Position becomes one EntityState in the blob.
//   * Tile-component owners (gx, gy, layer_id) become Tile entries.
//   * Lighting cells aren't streamed yet (added with the lighting layer's
//     in-memory representation in task #108).
//
// Order matters in FlatBuffers vector building: nested objects must finish
// BEFORE the table that owns them starts. We follow the canonical
// "tiles -> entities -> level_state" order used in the schema.
func EncodeMapState(in EncodeInputs) (EncodedMapState, error) {
	if in.Stores == nil {
		return nil, errors.New("persist: nil stores")
	}
	b := flatbuffers.NewBuilder(1024)

	// Strings + nested vectors first.
	instanceOffset := b.CreateString(in.InstanceID)

	tilesOffset := encodeTiles(b, in.Stores)
	entitiesOffset := encodeEntities(b, in.Stores)

	// ProtocolVersion is a (small) FlatBuffers TABLE, not a struct, so
	// we build it just like any other nested table — finish it before
	// MapStateStart.
	proto.ProtocolVersionStart(b)
	proto.ProtocolVersionAddMajor(b, ProtocolMajor)
	proto.ProtocolVersionAddMinor(b, ProtocolMinor)
	pvOffset := proto.ProtocolVersionEnd(b)

	proto.MapStateStart(b)
	proto.MapStateAddProtocolVersion(b, pvOffset)
	// FlatBuffers field name is preserved (MapId) for wire-protocol
	// compatibility; semantically this now carries a level id.
	proto.MapStateAddMapId(b, in.LevelID)
	proto.MapStateAddInstanceId(b, instanceOffset)
	proto.MapStateAddTick(b, in.Tick)
	proto.MapStateAddTiles(b, tilesOffset)
	proto.MapStateAddEntities(b, entitiesOffset)
	root := proto.MapStateEnd(b)

	proto.FinishMapStateBuffer(b, root)
	return EncodedMapState(b.FinishedBytes()), nil
}

// DecodeMapState reads a MapState blob and returns its top-level fields.
// Callers iterate Tiles/Entities/Lighting via the returned *proto.MapState
// directly; we don't pre-decode every field into Go structs because the
// recovery path applies them straight back into stores.
func DecodeMapState(blob []byte) (*proto.MapState, error) {
	// FlatBuffers identifier sits at offset 4..7. Reject anything shorter
	// to avoid the slice-out-of-range panic the generated helper would
	// otherwise raise.
	if len(blob) < 8 {
		return nil, errors.New("persist: MapState blob too short")
	}
	if !proto.MapStateBufferHasIdentifier(blob) {
		return nil, errors.New("persist: blob missing MapState identifier")
	}
	ms := proto.GetRootAsMapState(blob, 0)

	pv := ms.ProtocolVersion(nil)
	if pv == nil {
		return nil, errors.New("persist: blob missing protocol_version")
	}
	if pv.Major() != ProtocolMajor {
		return nil, fmt.Errorf("persist: protocol major %d != supported %d", pv.Major(), ProtocolMajor)
	}
	return ms, nil
}

// ApplyMapState restores entities into the world from a decoded MapState.
// Used by the recovery boot path. Spawns fresh EntityIDs (the persisted
// ids belonged to the previous process; we don't try to preserve them).
//
// Returns the number of entities applied for telemetry.
func ApplyMapState(w *ecs.World, ms *proto.MapState) int {
	stores := w.Stores()
	count := 0
	for i := 0; i < ms.EntitiesLength(); i++ {
		var es proto.EntityState
		if !ms.Entities(&es, i) {
			continue
		}
		e := w.Spawn()
		stores.Position.Set(e, components.Position{X: es.X(), Y: es.Y()})
		// Sprite component is rebuilt from (type_id -> entity-type lookup)
		// at recovery wiring time; for the bare blob we just preserve the
		// id so the downstream wiring can attach the right asset.
		stores.Sprite.Set(e, components.Sprite{
			AnimID:    uint32(es.AnimId()),
			VariantID: es.VariantId(),
			Tint:      es.Tint(),
			Facing:    es.Facing(),
		})
		count++
	}
	for i := 0; i < ms.TilesLength(); i++ {
		var t proto.Tile
		if !ms.Tiles(&t, i) {
			continue
		}
		e := w.Spawn()
		stores.Tile.Set(e, components.Tile{
			LayerID: t.LayerId(),
			GX:      t.Gx(),
			GY:      t.Gy(),
		})
		stores.Static.Set(e, components.Static{})
		stores.Sprite.Set(e, components.Sprite{
			AssetID: t.AssetId(),
		})
		count++
	}
	return count
}

// ---- internals ----

func encodeTiles(b *flatbuffers.Builder, stores *ecs.Stores) flatbuffers.UOffsetT {
	tiles := stores.Tile.Owners()
	if len(tiles) == 0 {
		proto.MapStateStartTilesVector(b, 0)
		return b.EndVector(0)
	}
	offsets := make([]flatbuffers.UOffsetT, 0, len(tiles))
	for i, e := range tiles {
		t := stores.Tile.Dense()[i]
		// Look up the matching Sprite/Collider to fill the Tile struct.
		var asset uint32
		var frame uint16
		if sp := stores.Sprite.GetPtr(e); sp != nil {
			asset = sp.AssetID
			frame = sp.Frame
		}
		var edge uint8
		var mask uint32 = 1
		if c := stores.Collider.GetPtr(e); c != nil {
			mask = c.Mask
		}
		proto.TileStart(b)
		proto.TileAddLayerId(b, t.LayerID)
		proto.TileAddGx(b, t.GX)
		proto.TileAddGy(b, t.GY)
		proto.TileAddAssetId(b, asset)
		proto.TileAddFrame(b, frame)
		proto.TileAddEdgeCollisions(b, edge)
		proto.TileAddCollisionLayerMask(b, mask)
		offsets = append(offsets, proto.TileEnd(b))
	}
	proto.MapStateStartTilesVector(b, len(offsets))
	for i := len(offsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offsets[i])
	}
	return b.EndVector(len(offsets))
}

func encodeEntities(b *flatbuffers.Builder, stores *ecs.Stores) flatbuffers.UOffsetT {
	owners := stores.Position.Owners()
	if len(owners) == 0 {
		proto.MapStateStartEntitiesVector(b, 0)
		return b.EndVector(0)
	}
	offsets := make([]flatbuffers.UOffsetT, 0, len(owners))
	for i, e := range owners {
		// Skip tile entities — they're already in the Tiles vector.
		if stores.Tile.Has(e) {
			continue
		}
		pos := stores.Position.Dense()[i]
		var asset uint32
		var animID uint16
		var animFrame uint16
		var variantID uint16
		var tint uint32
		var facing uint8
		if sp := stores.Sprite.GetPtr(e); sp != nil {
			asset = sp.AssetID
			animID = uint16(sp.AnimID)
			variantID = sp.VariantID
			tint = sp.Tint
			facing = sp.Facing
		}
		_ = animFrame

		proto.EntityStateStart(b)
		proto.EntityStateAddId(b, uint64(e))
		proto.EntityStateAddX(b, pos.X)
		proto.EntityStateAddY(b, pos.Y)
		proto.EntityStateAddFacing(b, facing)
		proto.EntityStateAddAnimId(b, animID)
		proto.EntityStateAddAnimFrame(b, animFrame)
		proto.EntityStateAddVariantId(b, variantID)
		proto.EntityStateAddTint(b, tint)
		// EntityState's asset_id field is implicit via type_id in the schema;
		// for v1 recovery we also stash it on a parallel Sprite component.
		_ = asset
		offsets = append(offsets, proto.EntityStateEnd(b))
	}
	proto.MapStateStartEntitiesVector(b, len(offsets))
	for i := len(offsets) - 1; i >= 0; i-- {
		b.PrependUOffsetT(offsets[i])
	}
	return b.EndVector(len(offsets))
}
