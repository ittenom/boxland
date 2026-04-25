package components_test

import (
	"encoding/json"
	"errors"
	"testing"

	"boxland/server/internal/entities/components"
)

func TestDefault_RegistersBuiltins(t *testing.T) {
	r := components.Default()
	for _, k := range []components.Kind{
		components.KindPosition,
		components.KindVelocity,
		components.KindSprite,
		components.KindCollider,
	} {
		if !r.Has(k) {
			t.Errorf("expected %q to be registered", k)
		}
	}
}

func TestKinds_StableSortedOrder(t *testing.T) {
	r := components.Default()
	got := r.Kinds()
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Errorf("Kinds() not sorted at index %d: %v", i, got)
		}
	}
}

func TestRegister_DuplicatePanics(t *testing.T) {
	r := components.Default()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	def, _ := r.Get(components.KindPosition)
	r.Register(def)
}

func TestRegister_RequiresAllHooks(t *testing.T) {
	r := components.NewRegistry()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for missing hooks")
		}
	}()
	r.Register(components.Definition{Kind: "broken"}) // no Descriptor/Validate/Default/Decode
}

func TestPosition_RoundTrip(t *testing.T) {
	r := components.Default()
	def, _ := r.Get(components.KindPosition)
	body := []byte(`{"x":256,"y":-128}`)
	if err := def.Validate(body); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	got, err := def.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	p, ok := got.(components.Position)
	if !ok {
		t.Fatalf("Decode returned %T, want Position", got)
	}
	if p.X != 256 || p.Y != -128 {
		t.Errorf("got %+v", p)
	}
}

func TestSprite_VariantWithoutAssetRejected(t *testing.T) {
	r := components.Default()
	def, _ := r.Get(components.KindSprite)
	bad := []byte(`{"asset_id":0,"variant_id":3}`)
	if err := def.Validate(bad); err == nil {
		t.Error("expected validation error: variant_id without asset_id")
	}
}

func TestCollider_AnchorOutsideBoundsRejected(t *testing.T) {
	r := components.Default()
	def, _ := r.Get(components.KindCollider)
	bad := []byte(`{"w":16,"h":16,"anchor_x":99,"anchor_y":4}`)
	if err := def.Validate(bad); err == nil {
		t.Error("expected validation error: anchor outside bounds")
	}
}

func TestValidateAll_HappyPath(t *testing.T) {
	r := components.Default()
	in := map[components.Kind]json.RawMessage{
		components.KindPosition: []byte(`{"x":0,"y":0}`),
		components.KindVelocity: []byte(`{}`),
		components.KindCollider: []byte(`{"w":16,"h":16,"anchor_x":8,"anchor_y":12,"mask":1}`),
	}
	if err := r.ValidateAll(in); err != nil {
		t.Fatalf("ValidateAll: %v", err)
	}
}

func TestValidateAll_UnknownKindReturnsErrUnknown(t *testing.T) {
	r := components.Default()
	err := r.ValidateAll(map[components.Kind]json.RawMessage{
		"ghost": []byte(`{}`),
	})
	if !errors.Is(err, components.ErrUnknownKind) {
		t.Errorf("got %v, want ErrUnknownKind", err)
	}
}

func TestValidateAll_PropagatesValidationError(t *testing.T) {
	r := components.Default()
	err := r.ValidateAll(map[components.Kind]json.RawMessage{
		components.KindCollider: []byte(`{"w":4,"h":4,"anchor_x":99,"anchor_y":99}`),
	})
	if err == nil {
		t.Fatal("expected propagated validation error")
	}
}

func TestDescriptor_ReturnsNonEmptyForEveryConfigurableBuiltin(t *testing.T) {
	r := components.Default()
	// Tag-only components (e.g. Static) legitimately have no fields. Skip
	// them; for everything else, verify the descriptor is populated.
	tagOnly := map[components.Kind]bool{
		components.KindStatic: true,
	}
	for _, k := range r.Kinds() {
		if tagOnly[k] {
			continue
		}
		def, _ := r.Get(k)
		fields := def.Descriptor()
		if len(fields) == 0 {
			t.Errorf("component %q has empty descriptor (and is not in the tag-only allowlist)", k)
		}
	}
}
