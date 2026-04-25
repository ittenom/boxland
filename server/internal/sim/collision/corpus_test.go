package collision_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"boxland/server/internal/sim/collision"
)

// vectorWorld matches the JSON shape in /shared/test-vectors/collision.json.
type vectorWorld struct {
	Tiles []struct {
		GX                 int32  `json:"gx"`
		GY                 int32  `json:"gy"`
		EdgeCollisions     uint8  `json:"edge_collisions"`
		CollisionLayerMask uint32 `json:"collision_layer_mask"`
	} `json:"tiles"`
}

type vector struct {
	Name   string `json:"name"`
	World  vectorWorld `json:"world"`
	Entity struct {
		AABB [4]int32 `json:"aabb"`
		Mask uint32   `json:"mask"`
	} `json:"entity"`
	Delta                 [2]int32 `json:"delta"`
	ExpectedResolvedDelta [2]int32 `json:"expected_resolved_delta"`
}

type corpus struct {
	SchemaVersion int      `json:"$schema_version"`
	Description   string   `json:"description"`
	Vectors       []vector `json:"vectors"`
}

// loadCorpus reads /shared/test-vectors/collision.json relative to this
// test file so the path is independent of the working directory.
func loadCorpus(t *testing.T) corpus {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "shared", "test-vectors", "collision.json")
	raw, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var c corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("decode corpus: %v", err)
	}
	return c
}

func TestCollisionCorpus_SchemaVersion(t *testing.T) {
	c := loadCorpus(t)
	if c.SchemaVersion != 1 {
		t.Errorf("$schema_version: got %d, want 1", c.SchemaVersion)
	}
}

func TestCollisionCorpus_AllVectorsMatchWebRuntime(t *testing.T) {
	c := loadCorpus(t)
	if len(c.Vectors) == 0 {
		t.Skip("corpus is empty (vectors land in PLAN.md task #40)")
	}
	for _, v := range c.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			tiles := make([]collision.Tile, 0, len(v.World.Tiles))
			for _, jt := range v.World.Tiles {
				tiles = append(tiles, collision.Tile{
					GX:                 jt.GX,
					GY:                 jt.GY,
					EdgeCollisions:     jt.EdgeCollisions,
					CollisionLayerMask: jt.CollisionLayerMask,
				})
			}
			world := collision.BuildWorld(tiles)
			entity := collision.Entity{
				AABB: collision.AABB{
					Left: v.Entity.AABB[0], Top: v.Entity.AABB[1],
					Right: v.Entity.AABB[2], Bottom: v.Entity.AABB[3],
				},
				Mask: v.Entity.Mask,
			}
			res := collision.Move(&entity, v.Delta[0], v.Delta[1], world)

			if res.ResolvedDX != v.ExpectedResolvedDelta[0] ||
				res.ResolvedDY != v.ExpectedResolvedDelta[1] {
				t.Errorf("server diverged from corpus: got (%d, %d), want (%d, %d)",
					res.ResolvedDX, res.ResolvedDY,
					v.ExpectedResolvedDelta[0], v.ExpectedResolvedDelta[1])
			}
		})
	}
}
