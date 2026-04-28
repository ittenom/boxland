package persist_test

import (
	"errors"
	"testing"

	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim/ecs"
	"boxland/server/internal/sim/persist"
)

func newWorldWithSampleEntities(t *testing.T) *ecs.World {
	t.Helper()
	w := ecs.NewWorld()
	stores := w.Stores()

	// Two free entities (mob + player-like).
	for i, p := range []components.Position{{X: 1024, Y: 2048}, {X: -512, Y: 0}} {
		e := w.Spawn()
		stores.Position.Set(e, p)
		stores.Sprite.Set(e, components.Sprite{
			AnimID: uint32(i + 1), VariantID: uint16(i),
			Tint: 0xff0000ff,
		})
	}
	// One tile entity.
	te := w.Spawn()
	stores.Tile.Set(te, components.Tile{LayerID: 0, GX: 5, GY: 7})
	stores.Static.Set(te, components.Static{})
	stores.Sprite.Set(te, components.Sprite{AssetID: 42})
	return w
}

func TestEncodeDecode_Roundtrip(t *testing.T) {
	w := newWorldWithSampleEntities(t)
	blob, err := persist.EncodeMapState(persist.EncodeInputs{
		LevelID:    77,
		InstanceID: "live:77:0",
		Tick:       12345,
		Stores:     w.Stores(),
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("encode produced empty blob")
	}

	ms, err := persist.DecodeMapState(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ms.MapId() != 77 {
		t.Errorf("MapId: got %d, want 77", ms.MapId())
	}
	if string(ms.InstanceId()) != "live:77:0" {
		t.Errorf("InstanceId: got %q", string(ms.InstanceId()))
	}
	if ms.Tick() != 12345 {
		t.Errorf("Tick: got %d", ms.Tick())
	}
	if ms.EntitiesLength() != 2 {
		t.Errorf("Entities count: got %d, want 2", ms.EntitiesLength())
	}
	if ms.TilesLength() != 1 {
		t.Errorf("Tiles count: got %d, want 1", ms.TilesLength())
	}
}

func TestApplyMapState_RecreatesEntities(t *testing.T) {
	src := newWorldWithSampleEntities(t)
	blob, err := persist.EncodeMapState(persist.EncodeInputs{
		LevelID:    1, InstanceID: "i", Tick: 0, Stores: src.Stores(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ms, err := persist.DecodeMapState(blob)
	if err != nil {
		t.Fatal(err)
	}

	dst := ecs.NewWorld()
	count := persist.ApplyMapState(dst, ms)
	// Encoding policy: free entities go into Entities; tile entities go
	// into Tiles. ApplyMapState restores them into the same shape, so:
	// 2 free entities (with Position) + 1 tile entity (with Tile + Static)
	// = 3 applied entries, but only 2 own Position.
	if count != 3 {
		t.Errorf("applied count: got %d, want 3", count)
	}
	if dst.Stores().Position.Len() != 2 {
		t.Errorf("Position store len: got %d, want 2 (only free entities own Position)", dst.Stores().Position.Len())
	}
	if dst.Stores().Tile.Len() != 1 {
		t.Errorf("Tile store len: got %d, want 1", dst.Stores().Tile.Len())
	}
	if dst.Stores().Static.Len() != 1 {
		t.Errorf("Static store len: got %d, want 1", dst.Stores().Static.Len())
	}
}

func TestDecode_RejectsEmptyBlob(t *testing.T) {
	if _, err := persist.DecodeMapState(nil); err == nil {
		t.Error("expected error for empty blob")
	}
}

func TestDecode_RejectsBlobWithoutIdentifier(t *testing.T) {
	if _, err := persist.DecodeMapState([]byte("garbage")); err == nil {
		t.Error("expected error for blob without MapState identifier")
	}
}

func TestEncode_NilStoresRejected(t *testing.T) {
	_, err := persist.EncodeMapState(persist.EncodeInputs{})
	if err == nil {
		t.Error("expected error for nil stores")
	}
	if err != nil && !errors.Is(err, err) { // satisfy errors-pkg lint
		t.Skip()
	}
}
