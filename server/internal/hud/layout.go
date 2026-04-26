// Package hud is the per-realm player-facing HUD: typed layout schema,
// widget catalog, binding-ref parser, and the publish-time validator.
//
// One Layout per realm. Lives on maps.hud_layout_json (migration 0031).
// The Pixi-side renderer at web/src/render/hud.ts consumes the same
// layout shape (sent as a one-shot HudLayoutFrame on JoinMap).
//
// Widgets are Configurable[T]: their Descriptor() drives the generic
// form renderer (server/views/form.templ) so adding a widget kind costs
// no per-widget Templ work. See PLAN.md §1 "Configurable JSON pattern"
// + docs/adding-a-component.md.
package hud

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"boxland/server/internal/automations"
	"boxland/server/internal/configurable"
)

// LayoutVersion is the one supported layout version. Bump when the
// shape changes in a non-additive way; add a per-version decoder.
const LayoutVersion = 1

// Caps. Bound memory + render cost. The form renderer will refuse to
// add past these.
const (
	MaxWidgetsPerAnchor = 32
	MaxWidgetsPerRealm  = 128
	MaxKeyLen           = 64
	MaxLabelLen         = 64
	MaxTemplateLen      = 256
)

// Anchor enumerates the nine fixed positions a widget stack may be
// pinned to. Mirrored on the client (web/src/render/hud.ts). Stable;
// do not rename.
type Anchor string

const (
	AnchorTopLeft      Anchor = "top-left"
	AnchorTopCenter    Anchor = "top-center"
	AnchorTopRight     Anchor = "top-right"
	AnchorMidLeft      Anchor = "mid-left"
	AnchorMidCenter    Anchor = "mid-center"
	AnchorMidRight     Anchor = "mid-right"
	AnchorBottomLeft   Anchor = "bottom-left"
	AnchorBottomCenter Anchor = "bottom-center"
	AnchorBottomRight  Anchor = "bottom-right"
)

// AllAnchors is the canonical iteration order (top→bottom, left→right).
// Used by the authoring UI's anchor map and by deterministic JSON
// serialization (since Go maps don't iterate in order).
var AllAnchors = []Anchor{
	AnchorTopLeft, AnchorTopCenter, AnchorTopRight,
	AnchorMidLeft, AnchorMidCenter, AnchorMidRight,
	AnchorBottomLeft, AnchorBottomCenter, AnchorBottomRight,
}

func validAnchor(a Anchor) bool {
	for _, c := range AllAnchors {
		if c == a {
			return true
		}
	}
	return false
}

// StackDir is "vertical" or "horizontal". A vertical stack lays widgets
// top-to-bottom from the anchor origin; a horizontal stack lays them
// left-to-right. Mid-* and bottom-* anchors flip direction sensibly on
// the client.
type StackDir string

const (
	StackVertical   StackDir = "vertical"
	StackHorizontal StackDir = "horizontal"
)

// Stack is one anchor's contents.
type Stack struct {
	Dir     StackDir `json:"dir"`
	Gap     int      `json:"gap"`     // px between widgets, world pixels
	OffsetX int      `json:"offsetX"` // px from the anchor edge
	OffsetY int      `json:"offsetY"`
	Widgets []Widget `json:"widgets"`
}

// Widget is the typed envelope every widget shares. Type picks the
// Config struct via the registry; Config is decoded lazily so unknown
// widget kinds round-trip without panicking (we error at Validate).
//
// VisibleWhen reuses the existing automations Condition AST so designers
// already know the DSL. nil = always visible.
//
// Skin is an optional ui_panel asset id (from Todo 1's KindUIPanel)
// rendered as a 9-patch frame underneath the widget. 0 = no frame.
type Widget struct {
	Type        WidgetKind                 `json:"type"`
	Order       int                        `json:"order"`
	VisibleWhen *automations.ConditionNode `json:"visible_when,omitempty"`
	Skin        int64                      `json:"skin,omitempty"`
	Tint        uint32                     `json:"tint,omitempty"` // 0xRRGGBBAA, 0 = no tint
	Size        WidgetSize                 `json:"size,omitempty"`
	Config      json.RawMessage            `json:"config"`
}

// WidgetSize is one of three integer scales. Constrained so we never
// produce non-integer sprite scaling.
type WidgetSize string

const (
	WidgetSize1x WidgetSize = "1x"
	WidgetSize2x WidgetSize = "2x"
	WidgetSize3x WidgetSize = "3x"
)

func (s WidgetSize) Valid() bool {
	switch s {
	case "", WidgetSize1x, WidgetSize2x, WidgetSize3x:
		return true
	}
	return false
}

// Layout is the full per-realm HUD. Anchors[a] absent = nothing
// pinned to that anchor.
type Layout struct {
	V       int              `json:"v"`
	Anchors map[Anchor]Stack `json:"anchors"`
}

// NewEmpty returns the canonical empty layout (matches the migration's
// DEFAULT). Useful for tests + the seeder.
func NewEmpty() Layout {
	return Layout{V: LayoutVersion, Anchors: map[Anchor]Stack{}}
}

// NewStarter returns the suggested starter HUD: HP bar bottom-left,
// gold text-label top-right, mini-clock top-left. Intended to be
// applied by the future starter-pack seeder (P0 #4) so a fresh project
// has a working HUD example to fork. Validated against DefaultRegistry().
//
// Bindings reference flag:gold (the seeder must also create that flag)
// and entity:host:hp_pct (always available). No asset refs — text only
// — so the layout works without any uploaded assets. Designers can
// upgrade text labels to icon_counter widgets after dropping in their
// chosen icon asset.
func NewStarter() Layout {
	bar := []byte(`{"binding":"entity:host:hp_pct","fill_color":4282623257,"label":"HP","show_value":true}`)
	clock := []byte(`{"channel":"realm_clock","format":"HH:MM"}`)
	gold := []byte(`{"template":"Gold: {flag:gold}","color":4294639871}`)
	return Layout{
		V: LayoutVersion,
		Anchors: map[Anchor]Stack{
			AnchorBottomLeft: {Dir: StackVertical, Gap: 2, OffsetX: 8, OffsetY: 8, Widgets: []Widget{
				{Type: WidgetResourceBar, Order: 0, Config: bar},
			}},
			AnchorTopLeft: {Dir: StackVertical, Gap: 2, OffsetX: 8, OffsetY: 8, Widgets: []Widget{
				{Type: WidgetMiniClock, Order: 0, Config: clock},
			}},
			AnchorTopRight: {Dir: StackVertical, Gap: 2, OffsetX: 8, OffsetY: 8, Widgets: []Widget{
				{Type: WidgetTextLabel, Order: 0, Config: gold},
			}},
		},
	}
}

// Decode parses a stored hud_layout_json blob. An empty / null blob
// returns NewEmpty() rather than an error — fresh maps have empty
// HUDs, not invalid ones.
func Decode(raw json.RawMessage) (Layout, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return NewEmpty(), nil
	}
	var l Layout
	if err := json.Unmarshal(raw, &l); err != nil {
		return Layout{}, fmt.Errorf("hud: decode layout: %w", err)
	}
	if l.Anchors == nil {
		l.Anchors = map[Anchor]Stack{}
	}
	return l, nil
}

// Validate enforces version, anchor whitelist, widget caps, and
// per-widget Validate(). It also decodes each Widget.Config into its
// typed struct so the publish path catches malformed widget configs
// before the WAL ever sees them.
//
// reg may be nil — in that case widget configs are *not* decoded
// (just structural validation runs). Production callers always pass
// the registry; tests of pure structure can omit it.
func (l Layout) Validate(reg *Registry) error {
	if l.V != LayoutVersion {
		return fmt.Errorf("hud: unsupported layout version %d (want %d)", l.V, LayoutVersion)
	}
	total := 0
	for anchor, stack := range l.Anchors {
		if !validAnchor(anchor) {
			return fmt.Errorf("hud: unknown anchor %q", anchor)
		}
		if stack.Dir != "" && stack.Dir != StackVertical && stack.Dir != StackHorizontal {
			return fmt.Errorf("hud: anchor %q: invalid dir %q", anchor, stack.Dir)
		}
		if stack.Gap < 0 || stack.Gap > 64 {
			return fmt.Errorf("hud: anchor %q: gap out of range", anchor)
		}
		if stack.OffsetX < 0 || stack.OffsetX > 1024 || stack.OffsetY < 0 || stack.OffsetY > 1024 {
			return fmt.Errorf("hud: anchor %q: offset out of range", anchor)
		}
		if len(stack.Widgets) > MaxWidgetsPerAnchor {
			return fmt.Errorf("hud: anchor %q: %d widgets exceeds %d cap", anchor, len(stack.Widgets), MaxWidgetsPerAnchor)
		}
		for i, w := range stack.Widgets {
			if !w.Size.Valid() {
				return fmt.Errorf("hud: anchor %q widget %d: invalid size %q", anchor, i, w.Size)
			}
			if w.Skin < 0 {
				return fmt.Errorf("hud: anchor %q widget %d: skin must be >= 0", anchor, i)
			}
			if w.VisibleWhen != nil {
				if err := w.VisibleWhen.Validate(); err != nil {
					return fmt.Errorf("hud: anchor %q widget %d: visible_when: %w", anchor, i, err)
				}
			}
			if reg != nil {
				if _, err := reg.Decode(w.Type, w.Config); err != nil {
					return fmt.Errorf("hud: anchor %q widget %d: %w", anchor, i, err)
				}
			}
			total++
		}
	}
	if total > MaxWidgetsPerRealm {
		return fmt.Errorf("hud: %d widgets exceeds per-realm cap %d", total, MaxWidgetsPerRealm)
	}
	return nil
}

// Descriptor satisfies configurable.Configurable for the *Layout itself*
// — currently a thin envelope; the per-widget descriptors are what the
// authoring form actually drives. Returned shape matches what the form
// renderer expects so the Layout can flow through the generic publish
// pipeline alongside everything else.
func (l Layout) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "v", Kind: configurable.KindInt, Label: "Layout version", Default: LayoutVersion},
	}
}

// Bindings walks the layout and returns every BindingRef referenced by
// any widget config. Used by the broadcaster to compute the binding-id
// table once at JoinMap and again at publish-time.
//
// The returned slice is sorted + deduped so binding ids are stable
// across publishes (a binding's id is its index in this list).
func (l Layout) Bindings(reg *Registry) ([]BindingRef, error) {
	if reg == nil {
		return nil, errors.New("hud: Bindings requires a registry")
	}
	seen := map[string]BindingRef{}
	for _, anchor := range AllAnchors {
		stack, ok := l.Anchors[anchor]
		if !ok {
			continue
		}
		for _, w := range stack.Widgets {
			cfg, err := reg.Decode(w.Type, w.Config)
			if err != nil {
				return nil, err
			}
			b, ok := cfg.(BindingProvider)
			if !ok {
				continue
			}
			for _, ref := range b.Bindings() {
				key := ref.String()
				if _, dup := seen[key]; !dup {
					seen[key] = ref
				}
			}
		}
	}
	out := make([]BindingRef, 0, len(seen))
	for _, ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}
