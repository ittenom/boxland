package systems_test

import (
	"context"
	"sync/atomic"
	"testing"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/sim"
	"boxland/server/internal/sim/ecs"
	"boxland/server/internal/sim/systems"
)

// stubCatalog returns canned animations and counts AnimationsFor calls
// so tests can assert the per-asset cache holds.
type stubCatalog struct {
	tables map[uint32]map[string]uint16
	calls  atomic.Int64
}

func (s *stubCatalog) AnimationsFor(_ context.Context, assetID uint32) (map[string]uint16, error) {
	s.calls.Add(1)
	out, ok := s.tables[assetID]
	if !ok {
		return nil, nil
	}
	// Lowercase keys so callers can match without re-casing.
	dup := make(map[string]uint16, len(out))
	for k, v := range out {
		dup[k] = v
	}
	return dup, nil
}

func makeWorldWithEntity(t *testing.T, vx, vy int32, assetID uint32) (*ecs.World, ecs.EntityID) {
	t.Helper()
	w := ecs.NewWorld()
	stores := w.Stores()
	e := w.Spawn()
	stores.Position.Set(e, components.Position{})
	stores.Velocity.Set(e, components.Velocity{VX: vx, VY: vy})
	stores.Sprite.Set(e, components.Sprite{AssetID: assetID})
	return w, e
}

func TestAnimation_PicksWalkClipFromVelocity(t *testing.T) {
	cat := &stubCatalog{tables: map[uint32]map[string]uint16{
		7: {"walk_north": 100, "walk_east": 101, "walk_south": 102, "walk_west": 103, "idle": 200},
	}}

	cases := []struct {
		name     string
		vx, vy   int32
		wantAnim uint32
		wantFace uint8
	}{
		{"east", 10, 0, 101, assets.FacingEast},
		{"west", -10, 0, 103, assets.FacingWest},
		{"north", 0, -10, 100, assets.FacingNorth},
		{"south", 0, 10, 102, assets.FacingSouth},
		{"diagonal NE -> east (horizontal wins on tie)", 10, -10, 101, assets.FacingEast},
		{"idle when stationary", 0, 0, 200, assets.FacingNorth /* sticky default 0 */},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, e := makeWorldWithEntity(t, c.vx, c.vy, 7)
			sch := sim.NewScheduler(w)
			sch.Register(systems.NewAnimationSystem(systems.AnimationSystemOptions{Catalog: cat}))
			if err := sch.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
			sp, _ := w.Stores().Sprite.Get(e)
			if sp.AnimID != c.wantAnim {
				t.Errorf("AnimID: got %d, want %d", sp.AnimID, c.wantAnim)
			}
			if sp.Facing != c.wantFace {
				t.Errorf("Facing: got %d, want %d", sp.Facing, c.wantFace)
			}
		})
	}
}

func TestAnimation_StationaryKeepsLastFacing(t *testing.T) {
	cat := &stubCatalog{tables: map[uint32]map[string]uint16{
		1: {"walk_east": 10, "idle": 99},
	}}
	w, e := makeWorldWithEntity(t, 100, 0, 1)
	sch := sim.NewScheduler(w)
	sch.Register(systems.NewAnimationSystem(systems.AnimationSystemOptions{Catalog: cat}))

	_ = sch.Step(context.Background())
	sp, _ := w.Stores().Sprite.Get(e)
	if sp.Facing != assets.FacingEast {
		t.Fatalf("setup: expected east facing, got %d", sp.Facing)
	}
	// Now stop. Facing should stay east; clip should switch to idle.
	v := w.Stores().Velocity.GetPtr(e)
	v.VX, v.VY = 0, 0
	_ = sch.Step(context.Background())
	sp, _ = w.Stores().Sprite.Get(e)
	if sp.Facing != assets.FacingEast {
		t.Errorf("stationary should preserve last facing; got %d", sp.Facing)
	}
	if sp.AnimID != 99 {
		t.Errorf("stationary should switch to idle (99); got %d", sp.AnimID)
	}
}

func TestAnimation_FallsBackThroughChain(t *testing.T) {
	// Asset has only `walk` and `idle` — no directional walks. East
	// movement should land on `walk`.
	cat := &stubCatalog{tables: map[uint32]map[string]uint16{
		2: {"walk": 50, "idle": 51},
	}}
	w, e := makeWorldWithEntity(t, 10, 0, 2)
	sch := sim.NewScheduler(w)
	sch.Register(systems.NewAnimationSystem(systems.AnimationSystemOptions{Catalog: cat}))

	_ = sch.Step(context.Background())
	sp, _ := w.Stores().Sprite.Get(e)
	if sp.AnimID != 50 {
		t.Errorf("fallback to walk: got %d, want 50", sp.AnimID)
	}
}

func TestAnimation_NoAnimationsLeavesAnimIDAlone(t *testing.T) {
	cat := &stubCatalog{tables: map[uint32]map[string]uint16{}}
	w, e := makeWorldWithEntity(t, 10, 0, 3)
	w.Stores().Sprite.Set(e, components.Sprite{AssetID: 3, AnimID: 42}) // pre-existing
	sch := sim.NewScheduler(w)
	sch.Register(systems.NewAnimationSystem(systems.AnimationSystemOptions{Catalog: cat}))
	_ = sch.Step(context.Background())
	sp, _ := w.Stores().Sprite.Get(e)
	if sp.AnimID != 42 {
		t.Errorf("missing catalog should not zero anim_id; got %d", sp.AnimID)
	}
	// Facing should still update though — useful for interaction logic.
	if sp.Facing != assets.FacingEast {
		t.Errorf("missing catalog should still update facing; got %d", sp.Facing)
	}
}

func TestAnimation_SkipsStaticEntities(t *testing.T) {
	cat := &stubCatalog{tables: map[uint32]map[string]uint16{1: {"walk_east": 10, "idle": 99}}}
	w := ecs.NewWorld()
	stores := w.Stores()
	e := w.Spawn()
	stores.Position.Set(e, components.Position{})
	stores.Velocity.Set(e, components.Velocity{VX: 50})
	stores.Sprite.Set(e, components.Sprite{AssetID: 1, AnimID: 7}) // pre-existing
	stores.Static.Set(e, components.Static{})
	sch := sim.NewScheduler(w)
	sch.Register(systems.NewAnimationSystem(systems.AnimationSystemOptions{Catalog: cat}))

	_ = sch.Step(context.Background())
	sp, _ := stores.Sprite.Get(e)
	if sp.AnimID != 7 || sp.Facing != 0 {
		t.Errorf("static tile should be untouched; got %+v", sp)
	}
}

func TestAnimation_CachesPerAssetLookups(t *testing.T) {
	cat := &stubCatalog{tables: map[uint32]map[string]uint16{
		1: {"walk_east": 10, "idle": 11},
	}}
	w := ecs.NewWorld()
	stores := w.Stores()
	for i := 0; i < 5; i++ {
		e := w.Spawn()
		stores.Position.Set(e, components.Position{})
		stores.Velocity.Set(e, components.Velocity{VX: 10})
		stores.Sprite.Set(e, components.Sprite{AssetID: 1})
	}
	sch := sim.NewScheduler(w)
	sch.Register(systems.NewAnimationSystem(systems.AnimationSystemOptions{Catalog: cat}))

	for i := 0; i < 3; i++ {
		_ = sch.Step(context.Background())
	}
	if got := cat.calls.Load(); got != 1 {
		t.Errorf("expected 1 catalog fetch for one asset across 5 entities × 3 ticks; got %d", got)
	}
}
