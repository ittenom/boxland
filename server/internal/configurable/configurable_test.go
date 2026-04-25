package configurable

import (
	"errors"
	"sort"
	"testing"
)

// healthConfig is a sample Configurable used to exercise the framework.
type healthConfig struct {
	HP       int     `json:"hp"`
	HPRegen  float64 `json:"hp_regen"`
	IsBoss   bool    `json:"is_boss"`
	Nameplate string `json:"nameplate"`
}

func (h healthConfig) Descriptor() []FieldDescriptor {
	min, max := 1.0, 100000.0
	return []FieldDescriptor{
		{Key: "hp", Label: "Lorem ipsum HP", Kind: KindInt, Required: true, Min: &min, Max: &max},
		{Key: "hp_regen", Label: "Regen / sec", Kind: KindFloat},
		{Key: "is_boss", Label: "Boss flag", Kind: KindBool},
		{Key: "nameplate", Label: "Nameplate", Kind: KindString, MaxLen: 32},
	}
}

func (h healthConfig) Validate() error {
	if h.HP < 1 {
		return errors.New("hp must be >= 1")
	}
	if h.HPRegen < 0 {
		return errors.New("hp_regen must be >= 0")
	}
	return nil
}

func TestConfigurableInterfaceShape(t *testing.T) {
	var c Configurable = healthConfig{HP: 100}
	if len(c.Descriptor()) != 4 {
		t.Errorf("descriptor count: got %d, want 4", len(c.Descriptor()))
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate(valid): %v", err)
	}
	if err := (healthConfig{HP: 0}).Validate(); err == nil {
		t.Error("Validate(invalid) should fail")
	}
}

func TestDiffJSON_NoChanges(t *testing.T) {
	a := healthConfig{HP: 100, HPRegen: 1.5, IsBoss: false, Nameplate: "x"}
	b := a
	d := DiffJSON(a, b)
	if len(d.Changes) != 0 {
		t.Errorf("expected no changes, got %+v", d.Changes)
	}
	if d.SummaryLine != "no changes" {
		t.Errorf("SummaryLine: %q", d.SummaryLine)
	}
}

func TestDiffJSON_SingleUpdate(t *testing.T) {
	a := healthConfig{HP: 100}
	b := healthConfig{HP: 200}
	d := DiffJSON(a, b)
	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %+v", d.Changes)
	}
	if d.Changes[0].Path != "hp" || d.Changes[0].Op != "updated" {
		t.Errorf("unexpected change: %+v", d.Changes[0])
	}
	if d.SummaryLine != "updated hp" {
		t.Errorf("SummaryLine: %q", d.SummaryLine)
	}
}

func TestDiffJSON_AddedRemoved(t *testing.T) {
	prev := map[string]any{"a": 1}
	next := map[string]any{"b": 2}
	d := DiffJSON(prev, next)
	sort.Slice(d.Changes, func(i, j int) bool { return d.Changes[i].Path < d.Changes[j].Path })
	if len(d.Changes) != 2 {
		t.Fatalf("got %+v", d.Changes)
	}
	if d.Changes[0].Path != "a" || d.Changes[0].Op != "removed" {
		t.Errorf("first: %+v", d.Changes[0])
	}
	if d.Changes[1].Path != "b" || d.Changes[1].Op != "added" {
		t.Errorf("second: %+v", d.Changes[1])
	}
}

func TestFieldKindStability(t *testing.T) {
	// These string values are part of the wire contract with the form
	// renderer (task #33). Don't rename without coordinating both sides.
	want := map[FieldKind]string{
		KindString:        "string",
		KindMultilineText: "text",
		KindInt:           "int",
		KindFloat:         "float",
		KindBool:          "bool",
		KindEnum:          "enum",
		KindAssetRef:      "asset_ref",
		KindEntityTypeRef: "entity_type_ref",
		KindColor:         "color",
		KindVec2:          "vec2",
		KindRange:         "range",
		KindNested:        "nested",
		KindList:          "list",
	}
	for k, v := range want {
		if string(k) != v {
			t.Errorf("FieldKind drift: %v != %q", k, v)
		}
	}
}
