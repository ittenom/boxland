package components_test

import (
	"encoding/json"
	"errors"
	"testing"

	"boxland/server/internal/entities/components"
)

func TestNineSlice_Validate_Positive(t *testing.T) {
	n := components.NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 6}
	if err := n.Validate(); err != nil {
		t.Fatalf("expected positive insets to validate, got %v", err)
	}
}

func TestNineSlice_Validate_RejectsNonPositive(t *testing.T) {
	cases := []struct {
		name string
		ns   components.NineSlice
	}{
		{"zero left", components.NineSlice{Left: 0, Top: 6, Right: 6, Bottom: 6}},
		{"zero top", components.NineSlice{Left: 6, Top: 0, Right: 6, Bottom: 6}},
		{"zero right", components.NineSlice{Left: 6, Top: 6, Right: 0, Bottom: 6}},
		{"zero bottom", components.NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 0}},
		{"negative", components.NineSlice{Left: -1, Top: 6, Right: 6, Bottom: 6}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.ns.Validate()
			if !errors.Is(err, components.ErrInvalidNineSlice) {
				t.Fatalf("want ErrInvalidNineSlice, got %v", err)
			}
		})
	}
}

func TestNineSlice_RoundTripsThroughRegistry(t *testing.T) {
	r := components.Default()
	def, ok := r.Get(components.KindNineSlice)
	if !ok {
		t.Fatal("KindNineSlice not registered in Default()")
	}
	raw, err := json.Marshal(components.NineSlice{Left: 4, Top: 5, Right: 6, Bottom: 7})
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Validate(raw); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	v, err := def.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got, ok := v.(components.NineSlice)
	if !ok {
		t.Fatalf("Decode returned %T, want NineSlice", v)
	}
	if got.Left != 4 || got.Top != 5 || got.Right != 6 || got.Bottom != 7 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestNineSlice_RegistryValidate_RejectsEmpty(t *testing.T) {
	r := components.Default()
	def, _ := r.Get(components.KindNineSlice)
	// Empty config is intentionally an error for nine_slice — every
	// UI sprite must declare its insets, there's no useful default
	// the renderer can fall back to without distorting the art.
	if err := def.Validate(json.RawMessage{}); err == nil {
		t.Fatal("expected empty config to fail validation")
	}
}

func TestNineSlice_DefaultIsValid(t *testing.T) {
	r := components.Default()
	def, _ := r.Get(components.KindNineSlice)
	d, ok := def.Default().(components.NineSlice)
	if !ok {
		t.Fatalf("Default() returned %T, want NineSlice", def.Default())
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("default value fails validation: %v", err)
	}
}
