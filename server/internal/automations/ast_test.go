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
