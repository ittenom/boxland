package hud

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"boxland/server/internal/flags"
)

func TestParseBinding_Entity(t *testing.T) {
	cases := []struct {
		in    string
		ok    bool
		want  BindingRef
	}{
		{"entity:host:hp_pct", true, BindingRef{Kind: BindEntity, Key: "host", Sub: "hp_pct"}},
		{"entity:self:nameplate", true, BindingRef{Kind: BindEntity, Key: "self", Sub: "nameplate"}},
		{"entity:host:resource:mana", false, BindingRef{}}, // 4 parts not allowed; future syntax
		{"entity:host", false, BindingRef{}},
		{"entity:nobody:hp_pct", false, BindingRef{}},
		{"entity:host:totally_made_up_field", false, BindingRef{}},
	}
	for _, c := range cases {
		got, err := ParseBinding(c.in)
		if c.ok && err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if !c.ok && err == nil {
			t.Errorf("%q: expected error, got %+v", c.in, got)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("%q: got %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestParseBinding_Flag(t *testing.T) {
	if _, err := ParseBinding("flag:gold"); err != nil {
		t.Fatalf("flag:gold: %v", err)
	}
	if _, err := ParseBinding("flag:Has-Quest"); err == nil {
		t.Fatal("flag:Has-Quest: expected error (uppercase + dash forbidden)")
	}
	if _, err := ParseBinding("flag:"); err == nil {
		t.Fatal("flag with empty key: expected error")
	}
	long := strings.Repeat("a", MaxKeyLen+1)
	if _, err := ParseBinding("flag:" + long); err == nil {
		t.Fatal("over-length flag key: expected error")
	}
}

func TestParseBinding_Time(t *testing.T) {
	if _, err := ParseBinding("time:realm_clock"); err != nil {
		t.Fatalf("time:realm_clock: %v", err)
	}
	if _, err := ParseBinding("time:wall"); err != nil {
		t.Fatalf("time:wall: %v", err)
	}
	if _, err := ParseBinding("time:nope"); err == nil {
		t.Fatal("time:nope: expected error")
	}
}

func TestBindingRef_StringRoundTrip(t *testing.T) {
	cases := []string{"entity:host:hp_pct", "flag:gold", "time:realm_clock"}
	for _, in := range cases {
		ref, err := ParseBinding(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := ref.String(); got != in {
			t.Errorf("round-trip %q -> %q", in, got)
		}
	}
}

func TestParseTemplateBindings(t *testing.T) {
	tmpl := "Gold: {flag:gold}  HP: {entity:host:hp_pct}"
	refs, err := ParseTemplateBindings(tmpl)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2", len(refs))
	}
	if refs[0].String() != "flag:gold" || refs[1].String() != "entity:host:hp_pct" {
		t.Errorf("refs %+v", refs)
	}
}

func TestParseTemplateBindings_UnterminatedFails(t *testing.T) {
	_, err := ParseTemplateBindings("Gold: {flag:gold")
	if err == nil {
		t.Fatal("expected error for unterminated brace")
	}
}

func TestRegistry_DecodeAndValidate(t *testing.T) {
	reg := DefaultRegistry()
	cases := []struct {
		kind    WidgetKind
		raw     string
		wantErr bool
	}{
		{WidgetResourceBar, `{"binding":"entity:host:hp_pct"}`, false},
		{WidgetResourceBar, `{"binding":"flag:gold","max":999}`, false},
		{WidgetResourceBar, `{"binding":"flag:gold"}`, true}, // flag binding without max
		{WidgetTextLabel, `{"template":"Gold: {flag:gold}"}`, false},
		{WidgetTextLabel, `{"template":""}`, true},
		{WidgetMiniClock, `{"channel":"realm_clock"}`, false},
		{WidgetMiniClock, `{"channel":"sundial"}`, true},
		{WidgetIconCounter, `{"icon":42,"binding":"flag:gold"}`, false},
		{WidgetIconCounter, `{"binding":"flag:gold"}`, true}, // missing icon
		{WidgetButton, `{"label":"Save","action_group":"save_game"}`, false},
		{WidgetButton, `{"label":"Save"}`, true}, // missing action_group
		{WidgetDialogFrame, `{"width_px":120,"height_px":80}`, false},
	}
	for _, c := range cases {
		_, err := reg.Decode(c.kind, json.RawMessage(c.raw))
		if c.wantErr && err == nil {
			t.Errorf("%s %s: expected error", c.kind, c.raw)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s %s: %v", c.kind, c.raw, err)
		}
	}
}

func TestLayout_ValidateCaps(t *testing.T) {
	reg := DefaultRegistry()
	// Build an over-cap anchor stack.
	stack := Stack{Dir: StackVertical}
	for i := 0; i < MaxWidgetsPerAnchor+1; i++ {
		stack.Widgets = append(stack.Widgets, Widget{
			Type:   WidgetMiniClock,
			Order:  i,
			Config: json.RawMessage(`{"channel":"realm_clock"}`),
		})
	}
	l := Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{AnchorTopLeft: stack}}
	if err := l.Validate(reg); err == nil {
		t.Fatal("expected per-anchor cap error")
	}
}

func TestLayout_ValidateBadAnchor(t *testing.T) {
	reg := DefaultRegistry()
	l := Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{
		Anchor("nowhere"): {Dir: StackVertical},
	}}
	if err := l.Validate(reg); err == nil {
		t.Fatal("expected unknown anchor error")
	}
}

func TestLayout_DecodeEmpty(t *testing.T) {
	cases := [][]byte{nil, []byte(""), []byte("null"), []byte(`{"v":1,"anchors":{}}`)}
	for _, c := range cases {
		l, err := Decode(c)
		if err != nil {
			t.Fatalf("%q: %v", string(c), err)
		}
		if l.V != LayoutVersion {
			t.Errorf("%q: V=%d", string(c), l.V)
		}
		if l.Anchors == nil {
			t.Errorf("%q: nil anchors", string(c))
		}
	}
}

func TestLayout_BindingsDedup(t *testing.T) {
	reg := DefaultRegistry()
	bar := Widget{Type: WidgetResourceBar, Config: json.RawMessage(`{"binding":"flag:gold","max":999}`)}
	icon := Widget{Type: WidgetIconCounter, Config: json.RawMessage(`{"icon":1,"binding":"flag:gold"}`)}
	hp := Widget{Type: WidgetResourceBar, Config: json.RawMessage(`{"binding":"entity:host:hp_pct"}`)}
	l := Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{
		AnchorTopLeft:    {Dir: StackVertical, Widgets: []Widget{bar, hp}},
		AnchorBottomLeft: {Dir: StackVertical, Widgets: []Widget{icon}},
	}}
	refs, err := l.Bindings(reg)
	if err != nil {
		t.Fatalf("bindings: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs, want 2 (gold dedup'd): %v", len(refs), refs)
	}
	// Sorted: entity:host:hp_pct < flag:gold
	if refs[0].String() != "entity:host:hp_pct" || refs[1].String() != "flag:gold" {
		t.Errorf("order: %v", refs)
	}
}

// stubAuto implements AutoResolver for tests.
type stubAuto struct{ names []string }

func (s stubAuto) ListActionGroupNames(_ context.Context, _ int64) ([]string, error) {
	return s.names, nil
}

func TestResolveAndValidate_HappyPath(t *testing.T) {
	reg := DefaultRegistry()
	l := Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{
		AnchorBottomLeft: {Dir: StackVertical, Widgets: []Widget{
			{Type: WidgetResourceBar, Config: json.RawMessage(`{"binding":"flag:gold","max":999}`)},
			{Type: WidgetButton, Config: json.RawMessage(`{"label":"Save","action_group":"save_game"}`)},
		}},
	}}
	deps := ResolveDeps{
		FlagKeys:         map[string]flags.Kind{"gold": flags.KindInt},
		ActionGroupNames: map[string]struct{}{"save_game": {}},
	}
	if err := l.ResolveAndValidate(reg, deps); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestResolveAndValidate_MissingFlag(t *testing.T) {
	reg := DefaultRegistry()
	l := Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{
		AnchorTopRight: {Dir: StackVertical, Widgets: []Widget{
			{Type: WidgetIconCounter, Config: json.RawMessage(`{"icon":1,"binding":"flag:gold"}`)},
		}},
	}}
	err := l.ResolveAndValidate(reg, ResolveDeps{
		FlagKeys:         map[string]flags.Kind{},
		ActionGroupNames: map[string]struct{}{},
	})
	if err == nil || !strings.Contains(err.Error(), "gold") {
		t.Fatalf("expected missing-flag error mentioning gold, got %v", err)
	}
}

func TestResolveAndValidate_MissingActionGroup(t *testing.T) {
	reg := DefaultRegistry()
	l := Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{
		AnchorBottomCenter: {Dir: StackVertical, Widgets: []Widget{
			{Type: WidgetButton, Config: json.RawMessage(`{"label":"X","action_group":"undefined"}`)},
		}},
	}}
	err := l.ResolveAndValidate(reg, ResolveDeps{
		ActionGroupNames: map[string]struct{}{"other": {}},
	})
	if err == nil || !strings.Contains(err.Error(), "undefined") {
		t.Fatalf("expected missing-action-group error mentioning name, got %v", err)
	}
}

func TestResolveAndValidate_VisibleWhenError(t *testing.T) {
	reg := DefaultRegistry()
	// Inject a malformed condition (count_gt with no value/threshold).
	visible := json.RawMessage(`{"op":"count_gt"}`)
	w := Widget{
		Type:   WidgetMiniClock,
		Config: json.RawMessage(`{"channel":"realm_clock"}`),
	}
	if err := json.Unmarshal(visible, &w.VisibleWhen); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	l := Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{
		AnchorTopLeft: {Dir: StackVertical, Widgets: []Widget{w}},
	}}
	err := l.Validate(reg)
	if err == nil {
		t.Fatal("expected visible_when validation error")
	}
}

func TestNewStarter_ValidatesAgainstDefaultRegistry(t *testing.T) {
	reg := DefaultRegistry()
	l := NewStarter()
	if err := l.Validate(reg); err != nil {
		t.Fatalf("starter HUD must validate clean: %v", err)
	}
	if got := len(l.Anchors); got != 3 {
		t.Errorf("expected 3 anchors in starter HUD, got %d", got)
	}
}

func TestRegistry_UnknownKind(t *testing.T) {
	reg := DefaultRegistry()
	_, err := reg.Decode(WidgetKind("gizmo"), json.RawMessage(`{}`))
	if err == nil || !errors.Is(err, ErrUnknownWidget) {
		t.Fatalf("expected ErrUnknownWidget, got %v", err)
	}
}
