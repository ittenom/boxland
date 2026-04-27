package maps_test

import (
	"context"
	"errors"
	"testing"

	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps"
)

func TestSamplePatch_UpsertAndRoundTrip(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, _, _, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// No patch yet → ErrNoSamplePatch.
	if _, err := svc.SamplePatchByMap(ctx, mapID); !errors.Is(err, maps.ErrNoSamplePatch) {
		t.Fatalf("got %v, want ErrNoSamplePatch", err)
	}

	// Upsert.
	if err := svc.UpsertSamplePatch(ctx, maps.SamplePatchInput{
		MapID: mapID, LayerID: layerID, X: 1, Y: 2, Width: 4, Height: 4, PatternN: 2,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := svc.SamplePatchByMap(ctx, mapID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.X != 1 || got.Y != 2 || got.Width != 4 || got.Height != 4 || got.PatternN != 2 {
		t.Errorf("round-trip: %+v", got)
	}

	// Update in place.
	if err := svc.UpsertSamplePatch(ctx, maps.SamplePatchInput{
		MapID: mapID, LayerID: layerID, X: 0, Y: 0, Width: 8, Height: 8, PatternN: 3,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = svc.SamplePatchByMap(ctx, mapID)
	if got.Width != 8 || got.PatternN != 3 {
		t.Errorf("update: %+v", got)
	}

	// Delete.
	if err := svc.DeleteSamplePatch(ctx, mapID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.SamplePatchByMap(ctx, mapID); !errors.Is(err, maps.ErrNoSamplePatch) {
		t.Fatalf("after delete got %v, want ErrNoSamplePatch", err)
	}
}

func TestSamplePatch_RejectsInvalidDims(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, _, _, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	cases := []struct {
		name string
		in   maps.SamplePatchInput
	}{
		{"too-narrow", maps.SamplePatchInput{MapID: mapID, LayerID: layerID, X: 0, Y: 0, Width: 1, Height: 4}},
		{"too-tall",   maps.SamplePatchInput{MapID: mapID, LayerID: layerID, X: 0, Y: 0, Width: 4, Height: 64}},
		{"neg-x",      maps.SamplePatchInput{MapID: mapID, LayerID: layerID, X: -1, Y: 0, Width: 4, Height: 4}},
		{"bad-N",      maps.SamplePatchInput{MapID: mapID, LayerID: layerID, X: 0, Y: 0, Width: 4, Height: 4, PatternN: 7}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.UpsertSamplePatch(ctx, tc.in)
			if !errors.Is(err, maps.ErrSamplePatchInvalid) {
				t.Errorf("got %v, want ErrSamplePatchInvalid", err)
			}
		})
	}
}

func TestSamplePatch_LoadTilesReadsFromMapTiles(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, et2, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// Paint a 2x2: 1 2 / 2 1
	cells := []struct {
		x, y int32
		et   int64
	}{
		{0, 0, et1}, {1, 0, et2},
		{0, 1, et2}, {1, 1, et1},
	}
	for _, c := range cells {
		if _, err := pool.Exec(ctx, `
			INSERT INTO map_tiles (map_id, layer_id, x, y, entity_type_id)
			VALUES ($1, $2, $3, $4, $5)
		`, mapID, layerID, c.x, c.y, c.et); err != nil {
			t.Fatalf("paint (%d,%d): %v", c.x, c.y, err)
		}
	}
	if err := svc.UpsertSamplePatch(ctx, maps.SamplePatchInput{
		MapID: mapID, LayerID: layerID, X: 0, Y: 0, Width: 2, Height: 2,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	sample, err := svc.LoadSamplePatchTiles(ctx, mapID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sample.Width != 2 || sample.Height != 2 || len(sample.Tiles) != 4 {
		t.Fatalf("dims/len wrong: %+v", sample)
	}
	want := []int64{et1, et2, et2, et1}
	for i, w := range want {
		if int64(sample.Tiles[i]) != w {
			t.Errorf("cell %d = %d, want %d", i, sample.Tiles[i], w)
		}
	}
}

func TestSamplePatch_LockOverlayWinsOverMapTiles(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, et2, layerID := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	// Paint base 2x2 all et1.
	for y := int32(0); y < 2; y++ {
		for x := int32(0); x < 2; x++ {
			if _, err := pool.Exec(ctx, `
				INSERT INTO map_tiles (map_id, layer_id, x, y, entity_type_id)
				VALUES ($1, $2, $3, $4, $5)
			`, mapID, layerID, x, y, et1); err != nil {
				t.Fatalf("paint: %v", err)
			}
		}
	}
	// Lock (1,1) to et2 — should override the base in the loaded sample.
	if err := svc.LockCells(ctx, []maps.LockedCell{
		{MapID: mapID, LayerID: layerID, X: 1, Y: 1, EntityTypeID: et2},
	}); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if err := svc.UpsertSamplePatch(ctx, maps.SamplePatchInput{
		MapID: mapID, LayerID: layerID, X: 0, Y: 0, Width: 2, Height: 2,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	sample, err := svc.LoadSamplePatchTiles(ctx, mapID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if int64(sample.Tiles[3]) != et2 {
		t.Errorf("lock did not override base: cell (1,1) = %d, want %d", sample.Tiles[3], et2)
	}
}

func TestSamplePatch_TenantIsolated(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapA, _, _, layerA := procFixture(t, ctx, designerID, baseEtID, ents, svc)
	mB, err := svc.Create(ctx, maps.CreateInput{
		Name: "proc-b", Width: 4, Height: 4, Mode: "procedural",
		PersistenceMode: "persistent", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if err := svc.UpsertSamplePatch(ctx, maps.SamplePatchInput{
		MapID: mapA, LayerID: layerA, X: 0, Y: 0, Width: 2, Height: 2,
	}); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if _, err := svc.SamplePatchByMap(ctx, mB.ID); !errors.Is(err, maps.ErrNoSamplePatch) {
		t.Errorf("B saw A's patch: err = %v", err)
	}
}
