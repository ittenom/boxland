package automations_test

import (
	"encoding/json"
	"testing"

	"boxland/server/internal/automations"
	"boxland/server/internal/configurable"
)

func TestDefaultTriggers_AllKindsRegistered(t *testing.T) {
	r := automations.DefaultTriggers()
	want := []string{
		"entity_nearby", "entity_absent", "resource_threshold",
		"timer", "on_spawn", "on_death", "on_interact", "on_enter_tile",
		// Flag triggers + on-realm-enter (indie-RPG research §P1 #9).
		"flag_equals", "flag_threshold", "on_realm_enter",
	}
	for _, k := range want {
		if !r.Has(k) {
			t.Errorf("trigger %q not registered", k)
		}
	}
	got := r.Kinds()
	if len(got) != len(want) {
		t.Errorf("Kinds(): got %d, want %d", len(got), len(want))
	}
}

func TestDefaultActions_AllKindsRegistered(t *testing.T) {
	r := automations.DefaultActions()
	want := []string{
		"spawn", "despawn", "move_toward", "move_away",
		"set_speed", "set_sprite", "set_animation", "set_variant",
		"set_tint", "play_sound", "emit_light", "adjust_resource",
		// Flag actions (indie-RPG research §P1 #9).
		"set_flag", "add_to_flag",
		// Common events (indie-RPG research §P1 #10).
		"call_action_group",
	}
	for _, k := range want {
		if !r.Has(k) {
			t.Errorf("action %q not registered", k)
		}
	}
	got := r.Kinds()
	if len(got) != len(want) {
		t.Errorf("Kinds(): got %d, want %d", len(got), len(want))
	}
}

func TestRegistry_GetReturnsDefinition(t *testing.T) {
	r := automations.DefaultTriggers()
	def, ok := r.Get("timer")
	if !ok {
		t.Fatal("timer should be registered")
	}
	if def.Kind != "timer" {
		t.Errorf("Kind: got %q", def.Kind)
	}
	if len(def.Descriptor()) != 2 {
		t.Errorf("timer descriptor: want 2 fields, got %d", len(def.Descriptor()))
	}
}

func TestRegistry_DuplicateRegistrationPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate kind")
		}
	}()
	r := automations.NewRegistry()
	def := automations.Definition{
		Kind: "x",
		Descriptor: func() []configurable.FieldDescriptor { return nil },
		Validate:   func(_ json.RawMessage) error { return nil },
		Default:    func() any { return nil },
		Decode:     func(_ json.RawMessage) (any, error) { return nil, nil },
	}
	r.Register(def)
	r.Register(def) // panic
}

func TestRegistry_MissingHooksPanic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on missing hooks")
		}
	}()
	r := automations.NewRegistry()
	r.Register(automations.Definition{Kind: "y"})
}
