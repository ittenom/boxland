package automations

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Automation is the unit of behaviour: one trigger gates a list of
// actions, optionally filtered by a recursive Condition tree. PLAN.md
// §1 + §122-§124.
type Automation struct {
	Name       string          `json:"name"`
	Trigger    TriggerNode     `json:"trigger"`
	Conditions *ConditionNode  `json:"conditions,omitempty"`
	Actions    []ActionNode    `json:"actions"`
}

// TriggerNode binds a trigger kind to its config blob. The compiler
// (task #126) decodes the blob through the Triggers registry.
type TriggerNode struct {
	Kind   string          `json:"kind"`
	Config json.RawMessage `json:"config"`
}

// ActionNode binds an action kind to its config blob.
type ActionNode struct {
	Kind   string          `json:"kind"`
	Config json.RawMessage `json:"config"`
}

// ConditionOp enumerates the AND/OR/NOT/threshold operators.
type ConditionOp string

const (
	CondAnd            ConditionOp = "and"
	CondOr             ConditionOp = "or"
	CondNot            ConditionOp = "not"
	CondCountGT        ConditionOp = "count_gt"
	CondCountLT        ConditionOp = "count_lt"
	CondRangeWithin    ConditionOp = "range_within"
)

// ConditionNode is the recursive AST node. Leaf nodes carry a
// `subject` (e.g. "nearby_count") + numeric Min/Max.
type ConditionNode struct {
	Op       ConditionOp      `json:"op"`
	Children []ConditionNode  `json:"children,omitempty"`
	Subject  string           `json:"subject,omitempty"`
	Min      *float64         `json:"min,omitempty"`
	Max      *float64         `json:"max,omitempty"`
	Value    *float64         `json:"value,omitempty"`
}

// AutomationSet is the bundle persisted under entity_automations.automation_ast_json.
type AutomationSet struct {
	Automations []Automation `json:"automations"`
}

// ---- Validate ---------------------------------------------------------

// Validate walks the AST + delegates to each kind's Validate hook.
// Returns the first error encountered (callers usually only care about
// "valid or not"). Used by the EntityType artifact handler at save time.
func (s AutomationSet) Validate(triggers, actions *Registry) error {
	for i := range s.Automations {
		if err := s.Automations[i].Validate(triggers, actions); err != nil {
			return fmt.Errorf("automations[%d] (%s): %w", i, s.Automations[i].Name, err)
		}
	}
	return nil
}

// Validate one Automation.
func (a Automation) Validate(triggers, actions *Registry) error {
	if a.Trigger.Kind == "" {
		return errors.New("trigger required")
	}
	tdef, ok := triggers.Get(a.Trigger.Kind)
	if !ok {
		return fmt.Errorf("%w: trigger %q", ErrUnknownKind, a.Trigger.Kind)
	}
	if err := tdef.Validate(a.Trigger.Config); err != nil {
		return fmt.Errorf("trigger %q: %w", a.Trigger.Kind, err)
	}
	if a.Conditions != nil {
		if err := a.Conditions.Validate(); err != nil {
			return fmt.Errorf("conditions: %w", err)
		}
	}
	if len(a.Actions) == 0 {
		return errors.New("at least one action required")
	}
	for i, ac := range a.Actions {
		adef, ok := actions.Get(ac.Kind)
		if !ok {
			return fmt.Errorf("%w: action[%d] %q", ErrUnknownKind, i, ac.Kind)
		}
		if err := adef.Validate(ac.Config); err != nil {
			return fmt.Errorf("action[%d] %q: %w", i, ac.Kind, err)
		}
	}
	return nil
}

// Validate a Condition tree. Walks children + checks numeric bounds.
func (c ConditionNode) Validate() error {
	switch c.Op {
	case CondAnd, CondOr:
		if len(c.Children) == 0 {
			return fmt.Errorf("%s: at least one child required", c.Op)
		}
		for i := range c.Children {
			if err := c.Children[i].Validate(); err != nil {
				return fmt.Errorf("child[%d]: %w", i, err)
			}
		}
	case CondNot:
		if len(c.Children) != 1 {
			return errors.New("not: exactly one child required")
		}
		return c.Children[0].Validate()
	case CondCountGT, CondCountLT:
		if c.Subject == "" {
			return fmt.Errorf("%s: subject required", c.Op)
		}
		if c.Value == nil {
			return fmt.Errorf("%s: value required", c.Op)
		}
	case CondRangeWithin:
		if c.Subject == "" {
			return errors.New("range_within: subject required")
		}
		if c.Min == nil || c.Max == nil {
			return errors.New("range_within: min and max required")
		}
		if *c.Min > *c.Max {
			return errors.New("range_within: min > max")
		}
	default:
		return fmt.Errorf("unknown condition op %q", c.Op)
	}
	return nil
}

// Decode unmarshals the persisted JSON into an AutomationSet without
// validating. Use Validate() before persisting; use this when reading
// for execution (assume already-validated data).
func DecodeSet(raw json.RawMessage) (AutomationSet, error) {
	if len(raw) == 0 {
		return AutomationSet{}, nil
	}
	var s AutomationSet
	if err := json.Unmarshal(raw, &s); err != nil {
		return AutomationSet{}, err
	}
	return s, nil
}
