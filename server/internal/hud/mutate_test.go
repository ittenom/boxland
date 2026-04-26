package hud

import (
	"encoding/json"
	"testing"
)

func TestLayout_AddWidget_AppendsWithFreshOrder(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	o1, err := l.AddWidget(AnchorTopLeft, WidgetMiniClock, reg)
	if err != nil {
		t.Fatal(err)
	}
	if o1 != 0 {
		t.Errorf("first widget order = %d, want 0", o1)
	}
	o2, err := l.AddWidget(AnchorTopLeft, WidgetMiniClock, reg)
	if err != nil {
		t.Fatal(err)
	}
	if o2 != 1 {
		t.Errorf("second widget order = %d, want 1", o2)
	}
	if got := len(l.Anchors[AnchorTopLeft].Widgets); got != 2 {
		t.Errorf("widget count = %d, want 2", got)
	}
}

func TestLayout_AddWidget_RejectsBadAnchor(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	if _, err := l.AddWidget(Anchor("nowhere"), WidgetMiniClock, reg); err == nil {
		t.Fatal("expected unknown-anchor error")
	}
}

func TestLayout_AddWidget_RejectsUnknownKind(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	if _, err := l.AddWidget(AnchorTopLeft, WidgetKind("gizmo"), reg); err == nil {
		t.Fatal("expected unknown-widget error")
	}
}

func TestLayout_AddWidget_PerAnchorCap(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	for i := 0; i < MaxWidgetsPerAnchor; i++ {
		if _, err := l.AddWidget(AnchorTopLeft, WidgetMiniClock, reg); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := l.AddWidget(AnchorTopLeft, WidgetMiniClock, reg); err == nil {
		t.Fatal("expected anchor-full error")
	}
}

func TestLayout_RemoveWidget_DropsEmptyAnchor(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	o, _ := l.AddWidget(AnchorBottomRight, WidgetMiniClock, reg)
	l.RemoveWidget(AnchorBottomRight, o)
	if _, ok := l.Anchors[AnchorBottomRight]; ok {
		t.Error("empty anchor should be dropped from layout")
	}
}

func TestLayout_RemoveWidget_IdempotentForUnknown(t *testing.T) {
	l := NewEmpty()
	// Should not panic.
	l.RemoveWidget(AnchorTopLeft, 999)
	l.RemoveWidget(Anchor("nowhere"), 0)
}

func TestLayout_MoveWidget_SwapsOrder(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	o1, _ := l.AddWidget(AnchorTopLeft, WidgetMiniClock, reg)
	o2, _ := l.AddWidget(AnchorTopLeft, WidgetMiniClock, reg)
	if err := l.MoveWidget(AnchorTopLeft, o1, +1); err != nil {
		t.Fatal(err)
	}
	// After swap, what was o1 is now order = (old o2), and vice versa.
	stack := l.Anchors[AnchorTopLeft]
	got := []int{stack.Widgets[0].Order, stack.Widgets[1].Order}
	if got[0] == got[1] {
		t.Fatalf("orders collapsed: %v", got)
	}
	// o2 should now be the smaller order.
	if !((stack.Widgets[0].Order == o2 && stack.Widgets[1].Order == o1) ||
		(stack.Widgets[0].Order == o1 && stack.Widgets[1].Order == o2)) {
		t.Errorf("unexpected order set: %v", got)
	}
}

func TestLayout_MoveWidget_BoundaryNoOp(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	o, _ := l.AddWidget(AnchorTopLeft, WidgetMiniClock, reg)
	if err := l.MoveWidget(AnchorTopLeft, o, -1); err != nil {
		t.Fatalf("expected no-op at boundary, got %v", err)
	}
}

func TestLayout_SaveWidgetConfig_ValidatesBeforeWriting(t *testing.T) {
	reg := DefaultRegistry()
	l := NewEmpty()
	o, _ := l.AddWidget(AnchorTopLeft, WidgetTextLabel, reg)
	bad := WidgetEnvelopeUpdate{Config: json.RawMessage(`{"template":""}`)}
	if err := l.SaveWidgetConfig(AnchorTopLeft, o, WidgetTextLabel, bad, reg); err == nil {
		t.Fatal("expected validation error for empty template")
	}
	// Old config still in place.
	got := l.Anchors[AnchorTopLeft].Widgets[0].Config
	if string(got) == `{"template":""}` {
		t.Error("invalid config was persisted")
	}
}

func TestLayout_SaveStackMetadata(t *testing.T) {
	l := NewEmpty()
	if err := l.SaveStackMetadata(AnchorTopRight, StackHorizontal, 6, 12, 8); err != nil {
		t.Fatal(err)
	}
	stack := l.Anchors[AnchorTopRight]
	if stack.Dir != StackHorizontal || stack.Gap != 6 || stack.OffsetX != 12 || stack.OffsetY != 8 {
		t.Errorf("unexpected stack metadata: %+v", stack)
	}
	if err := l.SaveStackMetadata(AnchorTopRight, StackHorizontal, 999, 0, 0); err == nil {
		t.Error("expected gap-out-of-range error")
	}
}
