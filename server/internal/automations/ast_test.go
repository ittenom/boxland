package automations_test

import (
	"encoding/json"
	"strings"
	"testing"

	"boxland/server/internal/automations"
)

func TestAST_ValidatesUnknownTrigger(t *testing.T) {
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name: "test",
			Trigger: automations.TriggerNode{Kind: "bogus"},
			Actions: []automations.ActionNode{{Kind: "spawn"}},
		}},
	}
	err := set.Validate(automations.DefaultTriggers(), automations.DefaultActions())
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("expected unknown-trigger error, got %v", err)
	}
}

func TestAST_ValidatesUnknownAction(t *testing.T) {
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name: "test",
			Trigger: automations.TriggerNode{Kind: "on_spawn"},
			Actions: []automations.ActionNode{{Kind: "doof"}},
		}},
	}
	err := set.Validate(automations.DefaultTriggers(), automations.DefaultActions())
	if err == nil || !strings.Contains(err.Error(), "doof") {
		t.Errorf("expected unknown-action error, got %v", err)
	}
}

func TestAST_RequiresAtLeastOneAction(t *testing.T) {
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name: "test",
			Trigger: automations.TriggerNode{Kind: "on_spawn"},
		}},
	}
	err := set.Validate(automations.DefaultTriggers(), automations.DefaultActions())
	if err == nil {
		t.Error("expected error for empty actions")
	}
}

func TestAST_PropagatesTriggerConfigErrors(t *testing.T) {
	cfg, _ := json.Marshal(map[string]any{"interval_ms": 50})
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name:    "fast-timer",
			Trigger: automations.TriggerNode{Kind: "timer", Config: cfg},
			Actions: []automations.ActionNode{{
				Kind: "spawn",
				Config: json.RawMessage(`{"type_id": 1}`),
			}},
		}},
	}
	err := set.Validate(automations.DefaultTriggers(), automations.DefaultActions())
	if err == nil || !strings.Contains(err.Error(), "interval_ms") {
		t.Errorf("expected interval_ms error, got %v", err)
	}
}

func TestAST_ConditionsValidateRecursively(t *testing.T) {
	cfg, _ := json.Marshal(map[string]any{"type_id": 1})
	min := 0.0
	max := 10.0
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name:    "with-conditions",
			Trigger: automations.TriggerNode{Kind: "on_spawn"},
			Conditions: &automations.ConditionNode{
				Op: automations.CondAnd,
				Children: []automations.ConditionNode{
					{Op: automations.CondRangeWithin, Subject: "x", Min: &min, Max: &max},
				},
			},
			Actions: []automations.ActionNode{
				{Kind: "spawn", Config: cfg},
			},
		}},
	}
	if err := set.Validate(automations.DefaultTriggers(), automations.DefaultActions()); err != nil {
		t.Errorf("expected valid, got %v", err)
	}
}

func TestAST_ConditionsRejectInvertedRange(t *testing.T) {
	min := 10.0
	max := 0.0
	c := automations.ConditionNode{
		Op: automations.CondRangeWithin, Subject: "x", Min: &min, Max: &max,
	}
	if err := c.Validate(); err == nil {
		t.Error("expected min > max error")
	}
}

func TestCompile_DecodesConfigs(t *testing.T) {
	tcfg, _ := json.Marshal(map[string]any{"interval_ms": 1000})
	acfg, _ := json.Marshal(map[string]any{"type_id": 5})
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name: "spawner",
			Trigger: automations.TriggerNode{Kind: "timer", Config: tcfg},
			Actions: []automations.ActionNode{{Kind: "spawn", Config: acfg}},
		}},
	}
	got, err := automations.Compile(set, automations.DefaultTriggers(), automations.DefaultActions())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d compiled automations", len(got))
	}
	timer, ok := got[0].Trigger.Config.(automations.TimerConfig)
	if !ok {
		t.Fatalf("trigger config: got %T", got[0].Trigger.Config)
	}
	if timer.IntervalMs != 1000 {
		t.Errorf("interval: got %d, want 1000", timer.IntervalMs)
	}
}

// "Talk to NPC twice to unlock door" -- the canonical end-to-end shape
// for the v1 flag system. Compile must succeed AND surface typed
// FlagThresholdConfig + SetFlagConfig configs the runtime can use.
func TestCompile_FlagTriggerAndActionRoundTrip(t *testing.T) {
	tcfg, _ := json.Marshal(map[string]any{
		"key": "talked_to_king", "op": ">=", "value": 2,
	})
	acfg, _ := json.Marshal(map[string]any{
		"key": "door_unlocked", "kind": "bool", "bool_val": true,
	})
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name:    "unlock_door",
			Trigger: automations.TriggerNode{Kind: "flag_threshold", Config: tcfg},
			Actions: []automations.ActionNode{{Kind: "set_flag", Config: acfg}},
		}},
	}
	got, err := automations.Compile(set, automations.DefaultTriggers(), automations.DefaultActions())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	thresh, ok := got[0].Trigger.Config.(automations.FlagThresholdConfig)
	if !ok {
		t.Fatalf("trigger config: got %T", got[0].Trigger.Config)
	}
	if thresh.Key != "talked_to_king" || thresh.Op != ">=" || thresh.Value != 2 {
		t.Errorf("threshold: got %+v", thresh)
	}
	setFlag, ok := got[0].Actions[0].Config.(automations.SetFlagConfig)
	if !ok {
		t.Fatalf("action config: got %T", got[0].Actions[0].Config)
	}
	if setFlag.Key != "door_unlocked" || setFlag.Kind != "bool" || !setFlag.BoolVal {
		t.Errorf("set_flag: got %+v", setFlag)
	}
}

// add_to_flag must reject delta=0 at validate time, so a designer who
// leaves the field at the (uncommon, accidental) zero default sees the
// error during the publish round-trip rather than discovering at
// runtime that the trigger is a no-op.
func TestCompile_AddToFlag_RejectsZeroDelta(t *testing.T) {
	acfg, _ := json.Marshal(map[string]any{"key": "gold", "delta": 0})
	set := automations.AutomationSet{
		Automations: []automations.Automation{{
			Name:    "broken",
			Trigger: automations.TriggerNode{Kind: "on_spawn"},
			Actions: []automations.ActionNode{{Kind: "add_to_flag", Config: acfg}},
		}},
	}
	if err := set.Validate(automations.DefaultTriggers(), automations.DefaultActions()); err == nil {
		t.Error("expected validate to reject delta=0")
	}
}
