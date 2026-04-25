package artifact

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/configurable"
)

type fakeHandler struct {
	kind Kind
}

func (f fakeHandler) Kind() Kind                                 { return f.kind }
func (f fakeHandler) Validate(ctx context.Context, _ DraftRow) error { return nil }
func (f fakeHandler) Publish(ctx context.Context, _ pgx.Tx, _ DraftRow) (PublishResult, error) {
	return PublishResult{Op: OpUpdated, Diff: configurable.StructuredDiff{SummaryLine: "ok"}}, nil
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeHandler{kind: KindAsset})
	r.Register(fakeHandler{kind: KindMap})

	if h, ok := r.HandlerFor(KindAsset); !ok || h.Kind() != KindAsset {
		t.Errorf("Asset handler missing")
	}
	if h, ok := r.HandlerFor(KindMap); !ok || h.Kind() != KindMap {
		t.Errorf("Map handler missing")
	}
	if _, ok := r.HandlerFor(KindEntityType); ok {
		t.Errorf("EntityType should not be registered")
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeHandler{kind: KindAsset})

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register(fakeHandler{kind: KindAsset})
}

func TestKindStability(t *testing.T) {
	// These string values are part of the wire contract with design.fbs
	// ArtifactKind. Don't rename without updating both sides.
	want := map[Kind]string{
		KindAsset:          "asset",
		KindEntityType:     "entity_type",
		KindMap:            "map",
		KindPalette:        "palette",
		KindPaletteVariant: "palette_variant",
		KindEdgeSocketType: "edge_socket_type",
		KindTileGroup:      "tile_group",
	}
	for k, v := range want {
		if string(k) != v {
			t.Errorf("Kind drift: %v != %q", k, v)
		}
	}
}
