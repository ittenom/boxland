package hud

import (
	"encoding/json"
	"errors"
	"fmt"

	"boxland/server/internal/automations"
)

// Mutation helpers used by both the HTTP handlers and tests. Centralized
// so the layout invariants (sorted by order, capped, sane defaults)
// stay consistent across every mutation path.

// AddWidget appends a widget to the named anchor with a fresh
// (max+1) order. The widget gets a zero-valued config of its kind so
// the form renderer has something concrete to edit. Returns the
// assigned order so HTTP responses can redirect to the new editor.
func (l *Layout) AddWidget(anchor Anchor, kind WidgetKind, reg *Registry) (int, error) {
	if !validAnchor(anchor) {
		return 0, fmt.Errorf("add widget: unknown anchor %q", anchor)
	}
	cfg, err := reg.New(kind)
	if err != nil {
		return 0, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return 0, fmt.Errorf("add widget: marshal default: %w", err)
	}
	stack := l.Anchors[anchor]
	if stack.Dir == "" {
		stack.Dir = StackVertical
	}
	if len(stack.Widgets) >= MaxWidgetsPerAnchor {
		return 0, fmt.Errorf("add widget: anchor %q full (max %d)", anchor, MaxWidgetsPerAnchor)
	}
	order := nextOrder(stack.Widgets)
	stack.Widgets = append(stack.Widgets, Widget{
		Type:   kind,
		Order:  order,
		Config: raw,
	})
	if l.Anchors == nil {
		l.Anchors = map[Anchor]Stack{}
	}
	l.Anchors[anchor] = stack
	return order, nil
}

// RemoveWidget deletes the widget at (anchor, order). No error if the
// anchor or order is absent — DELETE is idempotent.
func (l *Layout) RemoveWidget(anchor Anchor, order int) {
	stack, ok := l.Anchors[anchor]
	if !ok {
		return
	}
	out := stack.Widgets[:0]
	for _, w := range stack.Widgets {
		if w.Order != order {
			out = append(out, w)
		}
	}
	stack.Widgets = out
	if len(stack.Widgets) == 0 {
		// Drop the anchor entirely so an empty anchor doesn't show up
		// in the editor with stale settings.
		delete(l.Anchors, anchor)
		return
	}
	l.Anchors[anchor] = stack
}

// MoveWidget shifts the widget at (anchor, order) up or down within
// its stack. dir is -1 (toward order 0) or +1 (away). No-op if at
// the bound.
func (l *Layout) MoveWidget(anchor Anchor, order, dir int) error {
	if dir != -1 && dir != 1 {
		return errors.New("move widget: dir must be -1 or +1")
	}
	stack, ok := l.Anchors[anchor]
	if !ok {
		return errors.New("move widget: anchor empty")
	}
	idx := -1
	for i, w := range stack.Widgets {
		if w.Order == order {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("move widget: order not found")
	}
	// Sort by order so swap math is stable.
	sortByOrder(stack.Widgets)
	idx = -1
	for i, w := range stack.Widgets {
		if w.Order == order {
			idx = i
			break
		}
	}
	swap := idx + dir
	if swap < 0 || swap >= len(stack.Widgets) {
		return nil // boundary: no-op
	}
	stack.Widgets[idx].Order, stack.Widgets[swap].Order = stack.Widgets[swap].Order, stack.Widgets[idx].Order
	l.Anchors[anchor] = stack
	return nil
}

// SaveWidgetConfig replaces the config + visible_when + skin / tint /
// size envelope of the widget at (anchor, order). Validates the new
// config against its registered descriptor before persisting.
func (l *Layout) SaveWidgetConfig(anchor Anchor, order int, kind WidgetKind, env WidgetEnvelopeUpdate, reg *Registry) error {
	stack, ok := l.Anchors[anchor]
	if !ok {
		return errors.New("save widget: anchor empty")
	}
	for i, w := range stack.Widgets {
		if w.Order != order {
			continue
		}
		if w.Type != kind {
			return fmt.Errorf("save widget: kind mismatch (have %q, got %q)", w.Type, kind)
		}
		// Validate before writing so a bad form submission rolls back.
		if _, err := reg.Decode(kind, env.Config); err != nil {
			return err
		}
		stack.Widgets[i].Config = env.Config
		stack.Widgets[i].VisibleWhen = env.VisibleWhen
		stack.Widgets[i].Skin = env.Skin
		stack.Widgets[i].Tint = env.Tint
		stack.Widgets[i].Size = env.Size
		l.Anchors[anchor] = stack
		return nil
	}
	return errors.New("save widget: order not found")
}

// SaveStackMetadata updates the dir / gap / offsets of an anchor stack.
// Creates the anchor if absent.
func (l *Layout) SaveStackMetadata(anchor Anchor, dir StackDir, gap, offsetX, offsetY int) error {
	if !validAnchor(anchor) {
		return fmt.Errorf("save stack: unknown anchor %q", anchor)
	}
	if dir != StackVertical && dir != StackHorizontal {
		return fmt.Errorf("save stack: invalid dir %q", dir)
	}
	if gap < 0 || gap > 64 {
		return errors.New("save stack: gap out of range (0..64)")
	}
	if offsetX < 0 || offsetX > 1024 || offsetY < 0 || offsetY > 1024 {
		return errors.New("save stack: offset out of range (0..1024)")
	}
	stack := l.Anchors[anchor]
	stack.Dir = dir
	stack.Gap = gap
	stack.OffsetX = offsetX
	stack.OffsetY = offsetY
	if l.Anchors == nil {
		l.Anchors = map[Anchor]Stack{}
	}
	l.Anchors[anchor] = stack
	return nil
}

// WidgetEnvelopeUpdate is the bundle of fields the form-save endpoint
// can change per widget. Type + Order are immutable from the form
// (use Move and AddWidget to change those).
type WidgetEnvelopeUpdate struct {
	Config      json.RawMessage
	VisibleWhen *automations.ConditionNode
	Skin        int64
	Tint        uint32
	Size        WidgetSize
}

// nextOrder returns the next free order slot. Stable so reordering
// during edit doesn't pull the rug out from under the editor.
func nextOrder(ws []Widget) int {
	max := -1
	for _, w := range ws {
		if w.Order > max {
			max = w.Order
		}
	}
	return max + 1
}

func sortByOrder(ws []Widget) {
	// Insertion sort — N is bounded by MaxWidgetsPerAnchor (32), so
	// this is faster than calling sort.Slice + the closure overhead.
	for i := 1; i < len(ws); i++ {
		j := i
		for j > 0 && ws[j-1].Order > ws[j].Order {
			ws[j-1], ws[j] = ws[j], ws[j-1]
			j--
		}
	}
}


