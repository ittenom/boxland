package automations

import (
	"fmt"
)

// CompiledAutomation is the runtime representation of an Automation
// after the publish-time compiler walks the AST. The sim package
// (internal/sim) consumes these to produce ECS systems.
//
// PLAN.md §1 (Automations): "no-code AST that compiles to ECS systems".
// PLAN.md §126: "Live execution has zero interpretation overhead".
//
// v1 keeps the compiler conservative: each Automation produces one
// pre-bound function holding decoded trigger + action configs + a
// pre-validated condition tree. The sim runs them in a fixed-cost
// loop without re-walking JSON.
type CompiledAutomation struct {
	Name       string
	Trigger    CompiledTrigger
	Conditions *CompiledCondition
	Actions    []CompiledAction
}

// CompiledTrigger is the typed config for a trigger plus its kind.
// Specific systems pattern-match on Kind (or Type-assert Config) to
// fire the right per-tick check.
type CompiledTrigger struct {
	Kind   string
	Config any // typed via the Triggers registry's Decode.
}

// CompiledAction is the typed config for an action plus its kind.
type CompiledAction struct {
	Kind   string
	Config any
}

// CompiledCondition mirrors the AST node shape but with typed numeric
// fields (no pointers; absence is encoded as Op == "").
type CompiledCondition struct {
	Op       ConditionOp
	Children []CompiledCondition
	Subject  string
	Min      float64
	Max      float64
	Value    float64
	HasMin   bool
	HasMax   bool
	HasValue bool
}

// Compile walks the AutomationSet and produces a slice of
// CompiledAutomations. Returns an error if any kind is unregistered
// or any config blob fails to decode.
//
// The result is independent of the registries (decoded configs are
// owned by the returned slice), so the caller can hot-swap definitions
// at publish time without invalidating already-compiled automations.
func Compile(set AutomationSet, triggers, actions *Registry) ([]CompiledAutomation, error) {
	out := make([]CompiledAutomation, 0, len(set.Automations))
	for i, a := range set.Automations {
		ca, err := compileOne(a, triggers, actions)
		if err != nil {
			return nil, fmt.Errorf("automation[%d] (%s): %w", i, a.Name, err)
		}
		out = append(out, ca)
	}
	return out, nil
}

func compileOne(a Automation, triggers, actions *Registry) (CompiledAutomation, error) {
	tdef, ok := triggers.Get(a.Trigger.Kind)
	if !ok {
		return CompiledAutomation{}, fmt.Errorf("%w: trigger %q", ErrUnknownKind, a.Trigger.Kind)
	}
	tcfg, err := tdef.Decode(a.Trigger.Config)
	if err != nil {
		return CompiledAutomation{}, fmt.Errorf("decode trigger %q: %w", a.Trigger.Kind, err)
	}

	out := CompiledAutomation{
		Name:    a.Name,
		Trigger: CompiledTrigger{Kind: a.Trigger.Kind, Config: tcfg},
	}
	if a.Conditions != nil {
		c, err := compileCondition(*a.Conditions)
		if err != nil {
			return CompiledAutomation{}, fmt.Errorf("conditions: %w", err)
		}
		out.Conditions = &c
	}
	compiled, err := compileActions(a.Actions, actions)
	if err != nil {
		return CompiledAutomation{}, err
	}
	out.Actions = compiled
	return out, nil
}

func compileCondition(n ConditionNode) (CompiledCondition, error) {
	out := CompiledCondition{
		Op:      n.Op,
		Subject: n.Subject,
	}
	if n.Min != nil {
		out.Min = *n.Min
		out.HasMin = true
	}
	if n.Max != nil {
		out.Max = *n.Max
		out.HasMax = true
	}
	if n.Value != nil {
		out.Value = *n.Value
		out.HasValue = true
	}
	for i, c := range n.Children {
		cc, err := compileCondition(c)
		if err != nil {
			return CompiledCondition{}, fmt.Errorf("child[%d]: %w", i, err)
		}
		out.Children = append(out.Children, cc)
	}
	return out, nil
}
