// Package configurable provides the descriptor framework that drives
// the generic form renderer (task #33) and the structured-diff machinery
// used by the publish pipeline (task #132).
//
// Every `*_json` column in the schema is backed by a Go struct that
// implements Configurable. The struct's Descriptor() returns a list of
// FieldDescriptor entries describing the editable fields; the form
// renderer consumes these to emit a styled, hotkey-consistent form
// without bespoke per-type templates.
//
// New component kinds, automation triggers, and automation actions get UI
// for free by implementing Configurable. See PLAN.md §4n.
package configurable

import (
	"encoding/json"
	"fmt"
)

// FieldKind enumerates the input vocabulary the renderer understands.
// Add new kinds here AND in web/src/.../form-renderer (task #33). Both
// sides must agree.
type FieldKind string

const (
	KindString        FieldKind = "string"
	KindMultilineText FieldKind = "text"
	KindInt           FieldKind = "int"
	KindFloat         FieldKind = "float"
	KindBool          FieldKind = "bool"
	KindEnum          FieldKind = "enum"
	KindAssetRef      FieldKind = "asset_ref"
	KindEntityTypeRef FieldKind = "entity_type_ref"
	KindColor         FieldKind = "color"        // 0xRRGGBBAA
	KindVec2          FieldKind = "vec2"         // {x, y} in fixed-point sub-pixels
	KindRange         FieldKind = "range"        // {min, max}
	KindNested        FieldKind = "nested"       // recurse into Children
	KindList          FieldKind = "list"         // list of nested Children
)

// EnumOption is one selectable value for KindEnum fields.
type EnumOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FieldDescriptor describes a single editable property. Renderers consume
// this to pick an input control, enforce constraints, and render labels.
//
// Constraints (Min/Max/Pattern) are advisory for the renderer (it should
// surface them as input attributes); Validate() on the Configurable is
// authoritative.
type FieldDescriptor struct {
	Key      string    `json:"key"`                // JSON key on the underlying struct
	Label    string    `json:"label"`              // human-readable; placeholder Lorem-Ipsum until copy.json fills in
	Help     string    `json:"help,omitempty"`     // optional helper text
	Kind     FieldKind `json:"kind"`
	Required bool      `json:"required,omitempty"`
	Default  any       `json:"default,omitempty"`

	// Numeric constraints (Int / Float / Range)
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	Step *float64 `json:"step,omitempty"`

	// String constraints
	MaxLen  int    `json:"max_len,omitempty"`
	Pattern string `json:"pattern,omitempty"` // regex hint for the renderer

	// Enum options (KindEnum)
	Options []EnumOption `json:"options,omitempty"`

	// Reference filters (KindAssetRef / KindEntityTypeRef): tag filter for the picker.
	RefTags []string `json:"ref_tags,omitempty"`

	// Nested fields (KindNested / KindList)
	Children []FieldDescriptor `json:"children,omitempty"`
}

// Configurable is implemented by every typed config struct (component
// configs, automation AST nodes, reset rules, palette recipes, etc.).
//
// Implementations should be value-typed where possible so Diff is cheap.
type Configurable interface {
	// Descriptor returns the field descriptors used by the form renderer.
	// Stable across runtime; safe to cache.
	Descriptor() []FieldDescriptor

	// Validate returns nil if the receiver is internally consistent.
	// Called on every save; must be deterministic and side-effect-free.
	Validate() error
}

// Differ is an optional extension. Implementations may compute their own
// structured diff against a previous value; the default DiffJSON helper
// suffices for most cases.
type Differ interface {
	Diff(prev any) StructuredDiff
}

// StructuredDiff is the per-artifact diff payload consumed by the publish
// pipeline (task #132) and the diff preview modal (task #134). Each Change
// is one user-visible delta; SummaryLine is the human-readable single-line
// summary that lands in publish_diffs.summary_line.
type StructuredDiff struct {
	SummaryLine string   `json:"summary_line"`
	Changes     []Change `json:"changes"`
}

// Change is a single field-level delta. Path is dot-separated for nested
// fields. Op is one of "added", "removed", "updated".
type Change struct {
	Path string `json:"path"`
	Op   string `json:"op"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

// DiffJSON is the default Diff implementation: marshals both sides to JSON
// and walks the result, emitting one Change per top-level field that differs.
// Good enough for v1; future work can produce per-leaf paths for nested types.
func DiffJSON(prev, next any) StructuredDiff {
	prevMap, _ := toMap(prev)
	nextMap, _ := toMap(next)
	out := StructuredDiff{}

	keys := make(map[string]struct{}, len(prevMap)+len(nextMap))
	for k := range prevMap {
		keys[k] = struct{}{}
	}
	for k := range nextMap {
		keys[k] = struct{}{}
	}
	for k := range keys {
		pv, pok := prevMap[k]
		nv, nok := nextMap[k]
		switch {
		case !pok && nok:
			out.Changes = append(out.Changes, Change{Path: k, Op: "added", To: nv})
		case pok && !nok:
			out.Changes = append(out.Changes, Change{Path: k, Op: "removed", From: pv})
		case !equalJSON(pv, nv):
			out.Changes = append(out.Changes, Change{Path: k, Op: "updated", From: pv, To: nv})
		}
	}
	out.SummaryLine = summarize(out.Changes)
	return out
}

func toMap(v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func equalJSON(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func summarize(changes []Change) string {
	switch len(changes) {
	case 0:
		return "no changes"
	case 1:
		return fmt.Sprintf("%s %s", changes[0].Op, changes[0].Path)
	default:
		return fmt.Sprintf("%d field changes", len(changes))
	}
}
