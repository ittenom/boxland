package views_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"boxland/server/internal/configurable"
	"boxland/server/views"
)

func renderForm(t *testing.T, props views.FormProps) string {
	t.Helper()
	var buf bytes.Buffer
	if err := views.Form(props).Render(context.Background(), &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func TestForm_RendersAllPrimitiveKinds(t *testing.T) {
	min, max := 1.0, 100.0
	fields := []configurable.FieldDescriptor{
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: 32},
		{Key: "bio", Label: "Bio", Kind: configurable.KindMultilineText},
		{Key: "hp", Label: "HP", Kind: configurable.KindInt, Min: &min, Max: &max},
		{Key: "speed", Label: "Speed", Kind: configurable.KindFloat},
		{Key: "is_boss", Label: "Boss", Kind: configurable.KindBool},
		{
			Key: "facing", Label: "Facing", Kind: configurable.KindEnum,
			Options: []configurable.EnumOption{{Value: "n", Label: "North"}, {Value: "s", Label: "South"}},
		},
		{Key: "tint", Label: "Tint", Kind: configurable.KindColor},
	}
	out := renderForm(t, views.FormProps{
		Action: "/design/widgets",
		Fields: fields,
		Values: map[string]any{
			"name":    "alpha",
			"hp":      42,
			"is_boss": true,
			"facing":  "s",
		},
	})

	for _, want := range []string{
		`name="name"`,
		`value="alpha"`,
		`required`,
		`maxlength="32"`,
		`type="number"`,
		`min="1"`,
		`max="100"`,
		`type="checkbox"`,
		`checked`,
		`<select`,
		`<option value="s" selected`,
		`type="color"`,
		`hx-post="/design/widgets"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing: %q", want)
		}
	}
}

func TestForm_RefAndVecAndRange(t *testing.T) {
	fields := []configurable.FieldDescriptor{
		{Key: "sprite", Label: "Sprite", Kind: configurable.KindAssetRef},
		{Key: "spawn", Label: "Spawn pt", Kind: configurable.KindEntityTypeRef},
		{Key: "anchor", Label: "Anchor", Kind: configurable.KindVec2},
		{Key: "hp_band", Label: "HP band", Kind: configurable.KindRange},
	}
	out := renderForm(t, views.FormProps{Action: "/x", Fields: fields, Values: map[string]any{
		"anchor":  map[string]any{"x": 8, "y": 4},
		"hp_band": map[string]any{"min": 1, "max": 100},
	}})
	for _, want := range []string{
		`data-bx-ref="asset"`,
		`data-bx-ref="entity-type"`,
		`name="anchor.x"`,
		`value="8"`,
		`name="hp_band.max"`,
		`value="100"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing: %q", want)
		}
	}
}

func TestForm_NestedAndList(t *testing.T) {
	fields := []configurable.FieldDescriptor{
		{
			Key: "movement", Label: "Movement", Kind: configurable.KindNested,
			Children: []configurable.FieldDescriptor{
				{Key: "speed", Label: "Speed", Kind: configurable.KindInt},
				{Key: "agile", Label: "Agile", Kind: configurable.KindBool},
			},
		},
		{
			Key: "drops", Label: "Drops", Kind: configurable.KindList,
			Children: []configurable.FieldDescriptor{
				{Key: "item", Label: "Item", Kind: configurable.KindAssetRef},
				{Key: "qty", Label: "Qty", Kind: configurable.KindInt},
			},
		},
	}
	out := renderForm(t, views.FormProps{
		Action: "/x",
		Fields: fields,
		Values: map[string]any{
			"movement": map[string]any{"speed": 5, "agile": true},
		},
	})
	if !strings.Contains(out, `name="movement.speed"`) {
		t.Errorf("nested key not prefixed: %s", out)
	}
	if !strings.Contains(out, `name="drops[0].item"`) {
		t.Errorf("list child key not prefixed: %s", out)
	}
	if !strings.Contains(out, `value="5"`) {
		t.Errorf("nested value not rendered")
	}
}

func TestForm_HelpTextAndCopySlots(t *testing.T) {
	out := renderForm(t, views.FormProps{
		Action: "/x",
		Fields: []configurable.FieldDescriptor{
			{Key: "name", Label: "Lorem ipsum name", Kind: configurable.KindString,
				Help: "Lorem ipsum help text"},
		},
	})
	if !strings.Contains(out, `data-copy-slot="label.name"`) {
		t.Errorf("missing copy slot for label")
	}
	if !strings.Contains(out, `data-copy-slot="help.name"`) {
		t.Errorf("missing copy slot for help text")
	}
	if !strings.Contains(out, "Lorem ipsum help text") {
		t.Errorf("help text not rendered")
	}
}
