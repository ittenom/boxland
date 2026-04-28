package hud

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"boxland/server/internal/configurable"
)

// WidgetKind enumerates the v1 catalog. Add new kinds here AND register
// a constructor in DefaultRegistry(). The form renderer derives its
// editor from the Configurable returned by the constructor; no per-kind
// Templ work is required (PLAN.md §1, docs/adding-a-component.md).
type WidgetKind string

const (
	WidgetResourceBar WidgetKind = "resource_bar"
	WidgetTextLabel   WidgetKind = "text_label"
	WidgetMiniClock   WidgetKind = "mini_clock"
	WidgetIconCounter WidgetKind = "icon_counter"
	WidgetPortrait    WidgetKind = "portrait"
	WidgetButton      WidgetKind = "button"
	WidgetDialogFrame WidgetKind = "dialog_frame"
)

// AllWidgetKinds is the iteration order used by the authoring UI's
// "add widget" picker.
var AllWidgetKinds = []WidgetKind{
	WidgetResourceBar,
	WidgetTextLabel,
	WidgetIconCounter,
	WidgetPortrait,
	WidgetButton,
	WidgetMiniClock,
	WidgetDialogFrame,
}

// Registry maps WidgetKind → constructor. The constructor returns a
// fresh, zero-valued Configurable for the kind so the form renderer can
// emit an empty form and so Decode can unmarshal into the right shape.
type Registry struct {
	ctors map[WidgetKind]func() configurable.Configurable
}

// NewRegistry returns an empty registry. Most callers want
// DefaultRegistry instead.
func NewRegistry() *Registry {
	return &Registry{ctors: map[WidgetKind]func() configurable.Configurable{}}
}

// DefaultRegistry is the v1 catalog. Pure function so tests can call it
// freely. Do not mutate the returned registry from outside the hud
// package — make a fresh registry if you need a custom catalog.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(WidgetResourceBar, func() configurable.Configurable { return &ResourceBarConfig{} })
	r.Register(WidgetTextLabel, func() configurable.Configurable { return &TextLabelConfig{} })
	r.Register(WidgetMiniClock, func() configurable.Configurable { return &MiniClockConfig{} })
	r.Register(WidgetIconCounter, func() configurable.Configurable { return &IconCounterConfig{} })
	r.Register(WidgetPortrait, func() configurable.Configurable { return &PortraitConfig{} })
	r.Register(WidgetButton, func() configurable.Configurable { return &ButtonConfig{} })
	r.Register(WidgetDialogFrame, func() configurable.Configurable { return &DialogFrameConfig{} })
	return r
}

// Register adds a widget kind to the registry. Panics on duplicate
// (registry construction is at process boot; a duplicate is a bug).
func (r *Registry) Register(kind WidgetKind, ctor func() configurable.Configurable) {
	if _, dup := r.ctors[kind]; dup {
		panic(fmt.Sprintf("hud: widget kind %q already registered", kind))
	}
	r.ctors[kind] = ctor
}

// New returns a fresh zero-value config for the kind. ErrUnknownWidget
// if the kind isn't registered.
func (r *Registry) New(kind WidgetKind) (configurable.Configurable, error) {
	ctor, ok := r.ctors[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownWidget, kind)
	}
	return ctor(), nil
}

// Decode unmarshals raw config JSON into the kind's typed struct AND
// runs Validate. Returns the typed Configurable on success.
func (r *Registry) Decode(kind WidgetKind, raw json.RawMessage) (configurable.Configurable, error) {
	cfg, err := r.New(kind)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("hud: decode %s: %w", kind, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("hud: validate %s: %w", kind, err)
	}
	return cfg, nil
}

// Kinds returns the registered kinds in registration order. Used by
// tests that want to enumerate the catalog.
func (r *Registry) Kinds() []WidgetKind {
	out := make([]WidgetKind, 0, len(r.ctors))
	for k := range r.ctors {
		out = append(out, k)
	}
	return out
}

// ErrUnknownWidget is returned by Registry methods when the kind isn't
// registered. Caller surfaces this as 400 in the HTTP layer.
var ErrUnknownWidget = errors.New("hud: unknown widget kind")

// ---- Widget configs ---------------------------------------------------

// ResourceBarConfig drives the resource_bar widget. Bound to either an
// entity resource (e.g. "entity:host:hp_pct") or a flag (e.g.
// "flag:gold"); when bound to a flag the explicit Max is required since
// flags carry no implicit upper bound.
type ResourceBarConfig struct {
	Binding   string `json:"binding"`
	Max       int32  `json:"max"`
	FillColor uint32 `json:"fill_color"` // 0xRRGGBBAA
	BgColor   uint32 `json:"bg_color"`
	Label     string `json:"label,omitempty"`
	ShowValue bool   `json:"show_value"`
	Segmented bool   `json:"segmented"` // draw N filled blocks instead of one bar
	WidthPx   int32  `json:"width_px"`  // 0 = use the skin's natural width
	HeightPx  int32  `json:"height_px"` // 0 = use the skin's natural height
}

func (c ResourceBarConfig) Validate() error {
	ref, err := ParseBinding(c.Binding)
	if err != nil {
		return err
	}
	// Flag-bound bars need an explicit Max (a "gold" int has no
	// natural ceiling; pct fields do).
	if ref.Kind == BindFlag && c.Max <= 0 {
		return errors.New("resource_bar: flag binding requires max > 0")
	}
	if c.Max < 0 {
		return errors.New("resource_bar: max must be >= 0")
	}
	if c.WidthPx < 0 || c.WidthPx > 1024 || c.HeightPx < 0 || c.HeightPx > 256 {
		return errors.New("resource_bar: width/height out of range")
	}
	if len(c.Label) > MaxLabelLen {
		return fmt.Errorf("resource_bar: label exceeds %d chars", MaxLabelLen)
	}
	return nil
}

func (c ResourceBarConfig) Bindings() []BindingRef {
	ref, err := ParseBinding(c.Binding)
	if err != nil {
		return nil
	}
	return []BindingRef{ref}
}

func (c ResourceBarConfig) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "binding", Kind: configurable.KindString, Label: "Binding", Required: true,
			Help: "entity:host:hp_pct  or  flag:gold"},
		{Key: "max", Kind: configurable.KindInt, Label: "Max value",
			Help: "Required when binding is a flag. Ignored for hp_pct (always 255)."},
		{Key: "fill_color", Kind: configurable.KindColor, Label: "Fill color"},
		{Key: "bg_color", Kind: configurable.KindColor, Label: "Background color"},
		{Key: "label", Kind: configurable.KindString, Label: "Label", MaxLen: MaxLabelLen},
		{Key: "show_value", Kind: configurable.KindBool, Label: "Show numeric value"},
		{Key: "segmented", Kind: configurable.KindBool, Label: "Segmented (blocks)"},
		{Key: "width_px", Kind: configurable.KindInt, Label: "Width (px)", Help: "0 = use skin width"},
		{Key: "height_px", Kind: configurable.KindInt, Label: "Height (px)", Help: "0 = use skin height"},
	}
}

// TextLabelConfig drives the text_label widget. The Template is plain
// text with {kind:key[:sub]} substitutions parsed at publish time.
type TextLabelConfig struct {
	Template string `json:"template"`
	Color    uint32 `json:"color"`
	FontSize int32  `json:"font_size"` // one of 12, 16, 24
	Align    string `json:"align"`     // left|center|right
}

func (c TextLabelConfig) Validate() error {
	if c.Template == "" {
		return errors.New("text_label: template required")
	}
	if len(c.Template) > MaxTemplateLen {
		return fmt.Errorf("text_label: template exceeds %d chars", MaxTemplateLen)
	}
	if _, err := ParseTemplateBindings(c.Template); err != nil {
		return err
	}
	switch c.FontSize {
	case 0, 12, 16, 24: // 0 → renderer uses default 16
	default:
		return fmt.Errorf("text_label: font_size must be 12, 16, or 24 (got %d)", c.FontSize)
	}
	switch c.Align {
	case "", "left", "center", "right":
	default:
		return fmt.Errorf("text_label: align must be left|center|right (got %q)", c.Align)
	}
	return nil
}

func (c TextLabelConfig) Bindings() []BindingRef {
	refs, err := ParseTemplateBindings(c.Template)
	if err != nil {
		return nil
	}
	return refs
}

func (c TextLabelConfig) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "template", Kind: configurable.KindString, Label: "Text template", Required: true,
			MaxLen: MaxTemplateLen,
			Help:   `Plain text with {flag:gold} or {entity:host:hp_pct} substitutions.`},
		{Key: "color", Kind: configurable.KindColor, Label: "Text color"},
		{Key: "font_size", Kind: configurable.KindEnum, Label: "Font size",
			Options: []configurable.EnumOption{
				{Value: "12", Label: "Small (12)"},
				{Value: "16", Label: "Medium (16)"},
				{Value: "24", Label: "Large (24)"},
			}},
		{Key: "align", Kind: configurable.KindEnum, Label: "Align",
			Options: []configurable.EnumOption{
				{Value: "left", Label: "Left"},
				{Value: "center", Label: "Center"},
				{Value: "right", Label: "Right"},
			}},
	}
}

// MiniClockConfig drives the mini_clock widget.
type MiniClockConfig struct {
	Channel string `json:"channel"` // realm_clock | wall
	Format  string `json:"format"`  // "HH:MM" | "Day N" | "tick"
	Color   uint32 `json:"color"`
}

func (c MiniClockConfig) Validate() error {
	switch c.Channel {
	case "realm_clock", "wall":
	default:
		return errors.New("mini_clock: channel must be realm_clock|wall")
	}
	switch c.Format {
	case "", "HH:MM", "Day N", "tick":
	default:
		return fmt.Errorf("mini_clock: unknown format %q", c.Format)
	}
	return nil
}

func (c MiniClockConfig) Bindings() []BindingRef {
	return []BindingRef{{Kind: BindTime, Key: c.Channel}}
}

func (c MiniClockConfig) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "channel", Kind: configurable.KindEnum, Label: "Time source", Required: true,
			Options: []configurable.EnumOption{
				{Value: "realm_clock", Label: "Realm clock (in-game)"},
				{Value: "wall", Label: "Wall clock (real time)"},
			}},
		{Key: "format", Kind: configurable.KindEnum, Label: "Format",
			Options: []configurable.EnumOption{
				{Value: "HH:MM", Label: "HH:MM"},
				{Value: "Day N", Label: "Day N"},
				{Value: "tick", Label: "Tick number"},
			}},
		{Key: "color", Kind: configurable.KindColor, Label: "Text color"},
	}
}

// IconCounterConfig is "icon + integer" — gold count, lives left, etc.
type IconCounterConfig struct {
	Icon      int64  `json:"icon"`     // asset id of a sprite/tile
	Binding   string `json:"binding"`  // flag:gold etc.
	Color     uint32 `json:"color"`
	Prefix    string `json:"prefix,omitempty"`
	Suffix    string `json:"suffix,omitempty"`
	PadDigits int32  `json:"pad_digits"` // 0 = no padding
}

func (c IconCounterConfig) Validate() error {
	if c.Icon <= 0 {
		return errors.New("icon_counter: icon asset required")
	}
	if _, err := ParseBinding(c.Binding); err != nil {
		return err
	}
	if c.PadDigits < 0 || c.PadDigits > 12 {
		return errors.New("icon_counter: pad_digits out of range (0–12)")
	}
	if len(c.Prefix) > 16 || len(c.Suffix) > 16 {
		return errors.New("icon_counter: prefix/suffix max 16 chars")
	}
	return nil
}

func (c IconCounterConfig) Bindings() []BindingRef {
	ref, err := ParseBinding(c.Binding)
	if err != nil {
		return nil
	}
	return []BindingRef{ref}
}

func (c IconCounterConfig) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "icon", Kind: configurable.KindAssetRef, Label: "Icon", Required: true,
			RefTags: []string{"sprite", "tile"}},
		{Key: "binding", Kind: configurable.KindString, Label: "Binding", Required: true,
			Help: "Usually flag:<key>"},
		{Key: "color", Kind: configurable.KindColor, Label: "Text color"},
		{Key: "prefix", Kind: configurable.KindString, Label: "Prefix", MaxLen: 16},
		{Key: "suffix", Kind: configurable.KindString, Label: "Suffix", MaxLen: 16},
		{Key: "pad_digits", Kind: configurable.KindInt, Label: "Pad digits"},
	}
}

// PortraitConfig is a static or variant-bound sprite frame.
type PortraitConfig struct {
	Asset   int64  `json:"asset"`
	Frame   int32  `json:"frame"`
	Binding string `json:"binding,omitempty"` // optional, e.g. entity:host:variant_id
}

func (c PortraitConfig) Validate() error {
	if c.Asset <= 0 {
		return errors.New("portrait: asset required")
	}
	if c.Frame < 0 {
		return errors.New("portrait: frame must be >= 0")
	}
	if c.Binding != "" {
		if _, err := ParseBinding(c.Binding); err != nil {
			return err
		}
	}
	return nil
}

func (c PortraitConfig) Bindings() []BindingRef {
	if c.Binding == "" {
		return nil
	}
	ref, err := ParseBinding(c.Binding)
	if err != nil {
		return nil
	}
	return []BindingRef{ref}
}

func (c PortraitConfig) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "asset", Kind: configurable.KindAssetRef, Label: "Sprite", Required: true,
			RefTags: []string{"sprite"}},
		{Key: "frame", Kind: configurable.KindInt, Label: "Frame index"},
		{Key: "binding", Kind: configurable.KindString, Label: "Variant binding (optional)",
			Help: "e.g. entity:host:variant_id to swap on palette change"},
	}
}

// ButtonConfig is a clickable widget that fires a named action group.
// The action group is validated at publish time against the realm's
// level_action_groups.
type ButtonConfig struct {
	Label         string `json:"label"`
	Hotkey        string `json:"hotkey,omitempty"` // free char like "Q", "E", or "F1"
	ActionGroup   string `json:"action_group"`     // name in level_action_groups
	HitPaddingPx  int32  `json:"hit_padding_px"`   // grow hit-area beyond visual; default 4
}

func (c ButtonConfig) Validate() error {
	if c.Label == "" {
		return errors.New("button: label required")
	}
	if len(c.Label) > MaxLabelLen {
		return fmt.Errorf("button: label exceeds %d chars", MaxLabelLen)
	}
	if c.ActionGroup == "" {
		return errors.New("button: action_group required")
	}
	if len(c.ActionGroup) > MaxKeyLen {
		return fmt.Errorf("button: action_group exceeds %d chars", MaxKeyLen)
	}
	if c.HitPaddingPx < 0 || c.HitPaddingPx > 64 {
		return errors.New("button: hit_padding_px out of range (0–64)")
	}
	if c.Hotkey != "" && len(c.Hotkey) > 8 {
		return errors.New("button: hotkey too long")
	}
	if c.Hotkey != "" && strings.ContainsAny(c.Hotkey, " \t\n\r") {
		return errors.New("button: hotkey must be a single key name")
	}
	return nil
}

func (c ButtonConfig) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "label", Kind: configurable.KindString, Label: "Label", Required: true, MaxLen: MaxLabelLen},
		{Key: "hotkey", Kind: configurable.KindString, Label: "Hotkey",
			Help: `Single key name, e.g. "Q", "E", "F1". Optional.`},
		{Key: "action_group", Kind: configurable.KindString, Label: "Action group", Required: true, MaxLen: MaxKeyLen,
			Help: `Name from this realm's "Common events". Validated at publish.`},
		{Key: "hit_padding_px", Kind: configurable.KindInt, Label: "Hit padding (px)",
			Help: "Grows the click/tap area beyond the visual. Mobile-friendly default: 4."},
	}
}

// DialogFrameConfig is a passive 9-patch panel for grouping. No
// bindings; Skin (on the Widget envelope) carries the panel asset.
type DialogFrameConfig struct {
	WidthPx  int32 `json:"width_px"`
	HeightPx int32 `json:"height_px"`
}

func (c DialogFrameConfig) Validate() error {
	if c.WidthPx < 0 || c.WidthPx > 1024 || c.HeightPx < 0 || c.HeightPx > 1024 {
		return errors.New("dialog_frame: width/height out of range")
	}
	return nil
}

func (c DialogFrameConfig) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "width_px", Kind: configurable.KindInt, Label: "Width (px)"},
		{Key: "height_px", Kind: configurable.KindInt, Label: "Height (px)"},
	}
}
