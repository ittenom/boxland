package maps_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/maps"
)

func TestMapConstraints_AddListAndDelete(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, _, _, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	idA, err := svc.AddMapConstraint(ctx, maps.AddMapConstraintInput{
		MapID:  mapID,
		Kind:   maps.ConstraintKindBorder,
		Params: json.RawMessage(`{"entity_type_id":` + intStr(baseEtID) + `,"edges":["top","bottom"]}`),
	})
	if err != nil {
		t.Fatalf("add border: %v", err)
	}
	idB, err := svc.AddMapConstraint(ctx, maps.AddMapConstraintInput{
		MapID:  mapID,
		Kind:   maps.ConstraintKindPath,
		Params: json.RawMessage(`{"entity_type_ids":[` + intStr(baseEtID) + `]}`),
	})
	if err != nil {
		t.Fatalf("add path: %v", err)
	}

	got, err := svc.MapConstraints(ctx, mapID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d constraints, want 2", len(got))
	}
	if got[0].Kind != maps.ConstraintKindBorder || got[1].Kind != maps.ConstraintKindPath {
		t.Errorf("kinds out of order: %+v", got)
	}

	// Delete the second.
	if err := svc.DeleteMapConstraint(ctx, mapID, idB); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ = svc.MapConstraints(ctx, mapID)
	if len(got) != 1 || got[0].ID != idA {
		t.Errorf("after delete: %+v", got)
	}
}

func TestMapConstraints_RejectsInvalidKind(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, _, _, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)
	_, err := svc.AddMapConstraint(ctx, maps.AddMapConstraintInput{
		MapID: mapID, Kind: "bogus", Params: json.RawMessage(`{}`),
	})
	if !errors.Is(err, maps.ErrConstraintInvalid) {
		t.Errorf("got %v, want ErrConstraintInvalid", err)
	}
}

func TestMapConstraints_RejectsBadBorderParams(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, _, _, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	cases := []struct{ name, params string }{
		{"missing-entity", `{"edges":["top"]}`},
		{"unknown-edge", `{"entity_type_id":` + intStr(baseEtID) + `,"edges":["diagonal"]}`},
		{"malformed-json", `{"entity_type_id":"oops"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.AddMapConstraint(ctx, maps.AddMapConstraintInput{
				MapID: mapID, Kind: maps.ConstraintKindBorder, Params: json.RawMessage(tc.params),
			})
			if !errors.Is(err, maps.ErrConstraintInvalid) {
				t.Errorf("got %v, want ErrConstraintInvalid", err)
			}
		})
	}
}

func TestMapConstraints_TenantIsolated(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapA, _, _, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)
	mB, err := svc.Create(ctx, maps.CreateInput{
		Name: "proc-b", Width: 4, Height: 4, Mode: "procedural",
		PersistenceMode: "persistent", CreatedBy: designerID,
	})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if _, err := svc.AddMapConstraint(ctx, maps.AddMapConstraintInput{
		MapID: mapA, Kind: maps.ConstraintKindBorder,
		Params: json.RawMessage(`{"entity_type_id":` + intStr(baseEtID) + `,"edges":["all"]}`),
	}); err != nil {
		t.Fatalf("add A: %v", err)
	}
	got, _ := svc.MapConstraints(ctx, mB.ID)
	if len(got) != 0 {
		t.Errorf("B saw A's constraints: %+v", got)
	}
}

func TestPreview_BorderConstraintIsHonored(t *testing.T) {
	pool := openTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	designerID, baseEtID := resetDB(t, pool)
	ents := entities.New(pool, components.Default())
	svc := maps.New(pool)
	mapID, et1, _, _ := procFixture(t, ctx, designerID, baseEtID, ents, svc)

	if _, err := svc.AddMapConstraint(ctx, maps.AddMapConstraintInput{
		MapID: mapID, Kind: maps.ConstraintKindBorder,
		Params: json.RawMessage(`{"entity_type_id":` + intStr(et1) + `,"edges":["top"]}`),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	res, err := svc.GenerateProceduralPreview(ctx, maps.ProceduralPreviewInput{
		MapID: mapID, Width: 4, Height: 4, Seed: 7,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for x := int32(0); x < 4; x++ {
		got := int64(res.Region.Cells[x].EntityType)
		if got != et1 {
			t.Errorf("top row x=%d = %d, want %d (border constraint not applied)", x, got, et1)
		}
	}
}

// intStr is a tiny helper so the JSON fixtures stay readable.
func intStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
