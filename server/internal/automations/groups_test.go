package automations_test

import (
	"encoding/json"
	"errors"
	"testing"

	"boxland/server/internal/automations"
)

// callTo builds an ActionNode that invokes the named common event.
func callTo(name string) automations.ActionNode {
	cfg, _ := json.Marshal(map[string]any{"name": name})
	return automations.ActionNode{Kind: "call_action_group", Config: cfg}
}

// spawn5 builds a leaf action so a group has at least one non-call op.
func spawn5() automations.ActionNode {
	cfg, _ := json.Marshal(map[string]any{"type_id": 5})
	return automations.ActionNode{Kind: "spawn", Config: cfg}
}

func TestActionGroups_HappyPath(t *testing.T) {
	groups := []automations.ActionGroup{
		{Name: "award_xp", Actions: []automations.ActionNode{spawn5()}},
		{Name: "victory",  Actions: []automations.ActionNode{callTo("award_xp"), spawn5()}},
	}
	out, err := automations.CompileActionGroups(groups, automations.DefaultActions())
	if err != nil {
		t.Fatalf("CompileActionGroups: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d groups, want 2", len(out))
	}
	if out["victory"].Actions[0].Kind != "call_action_group" {
		t.Errorf("victory[0]: kind = %q", out["victory"].Actions[0].Kind)
	}
}

func TestActionGroups_RejectsSelfCycle(t *testing.T) {
	groups := []automations.ActionGroup{
		{Name: "loop", Actions: []automations.ActionNode{callTo("loop")}},
	}
	_, err := automations.CompileActionGroups(groups, automations.DefaultActions())
	if !errors.Is(err, automations.ErrActionGroupCycle) {
		t.Fatalf("want ErrActionGroupCycle, got %v", err)
	}
}

func TestActionGroups_RejectsLongerCycle(t *testing.T) {
	// a -> b -> c -> a
	groups := []automations.ActionGroup{
		{Name: "a", Actions: []automations.ActionNode{callTo("b")}},
		{Name: "b", Actions: []automations.ActionNode{callTo("c")}},
		{Name: "c", Actions: []automations.ActionNode{callTo("a")}},
	}
	_, err := automations.CompileActionGroups(groups, automations.DefaultActions())
	if !errors.Is(err, automations.ErrActionGroupCycle) {
		t.Fatalf("want ErrActionGroupCycle, got %v", err)
	}
}

func TestActionGroups_AllowsDiamond(t *testing.T) {
	// a -> b, a -> c, b -> d, c -> d  (a diamond is NOT a cycle).
	groups := []automations.ActionGroup{
		{Name: "a", Actions: []automations.ActionNode{callTo("b"), callTo("c")}},
		{Name: "b", Actions: []automations.ActionNode{callTo("d")}},
		{Name: "c", Actions: []automations.ActionNode{callTo("d")}},
		{Name: "d", Actions: []automations.ActionNode{spawn5()}},
	}
	if _, err := automations.CompileActionGroups(groups, automations.DefaultActions()); err != nil {
		t.Errorf("diamond should compile: %v", err)
	}
}

func TestActionGroups_RejectsUnknownTarget(t *testing.T) {
	groups := []automations.ActionGroup{
		{Name: "a", Actions: []automations.ActionNode{callTo("ghost")}},
	}
	_, err := automations.CompileActionGroups(groups, automations.DefaultActions())
	if !errors.Is(err, automations.ErrActionGroupUnknown) {
		t.Fatalf("want ErrActionGroupUnknown, got %v", err)
	}
}

func TestActionGroups_RejectsDuplicateNames(t *testing.T) {
	groups := []automations.ActionGroup{
		{Name: "x", Actions: []automations.ActionNode{spawn5()}},
		{Name: "x", Actions: []automations.ActionNode{spawn5()}},
	}
	_, err := automations.CompileActionGroups(groups, automations.DefaultActions())
	if !errors.Is(err, automations.ErrActionGroupDuplicate) {
		t.Fatalf("want ErrActionGroupDuplicate, got %v", err)
	}
}

func TestCallActionGroup_RejectsEmptyName(t *testing.T) {
	cfg, _ := json.Marshal(map[string]any{"name": ""})
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name:    "broken_call",
			Trigger: automations.TriggerNode{Kind: "on_spawn"},
			Actions: []automations.ActionNode{{Kind: "call_action_group", Config: cfg}},
		}},
	}
	if err := set.Validate(automations.DefaultTriggers(), automations.DefaultActions()); err == nil {
		t.Error("expected validate to reject empty name")
	}
}

func TestMaxCallDepth_IsBounded(t *testing.T) {
	// The constant exists for the runtime; verify it's a sensible
	// small number so a misconfigured graph that slips past
	// CompileActionGroups still can't stall a tick.
	if automations.MaxCallDepth <= 0 || automations.MaxCallDepth > 8 {
		t.Errorf("MaxCallDepth = %d; want a small positive bound (<=8)", automations.MaxCallDepth)
	}
}
