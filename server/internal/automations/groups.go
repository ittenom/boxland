package automations

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ActionGroup is one row from map_action_groups, decoded.
//
// Indie-RPG research §P1 #10 ("common events"). Designers define a
// reusable action chain once and call it from many automations via the
// call_action_group action.
type ActionGroup struct {
	ID      int64
	MapID   int64
	Name    string
	Actions []ActionNode
}

// CompiledActionGroup mirrors the runtime CompiledAction layout for one
// group. Calls to other groups are NOT inlined here -- the runtime
// dispatcher resolves them by name with the depth-bounded recursion
// guard (MaxCallDepth). Inlining would be a viable v1 optimisation but
// blows up the compiled size for groups with diamond call graphs.
type CompiledActionGroup struct {
	Name    string
	Actions []CompiledAction
}

// CompiledActionGroups indexes compiled groups by name for O(1)
// dispatch from call_action_group at runtime.
type CompiledActionGroups map[string]CompiledActionGroup

// Errors returned by CompileActionGroups.
var (
	ErrActionGroupCycle      = errors.New("action_group: call cycle")
	ErrActionGroupUnknown    = errors.New("action_group: name not found")
	ErrActionGroupDuplicate  = errors.New("action_group: duplicate name")
)

// CompileActionGroups compiles a slice of ActionGroups into a name->
// CompiledActionGroup index. It performs three publish-time checks
// designers benefit from seeing BEFORE the realm goes live:
//
//   1. Duplicate names -- the (map_id, name) UNIQUE constraint catches
//      this at the DB layer too, but we want a typed error before the
//      INSERT fires.
//   2. Unknown call targets -- a call_action_group action whose `name`
//      doesn't resolve to any group.
//   3. Cycles -- A->B->A, A->A, or longer chains. We use Tarjan-style
//      DFS coloring (white/gray/black) on the directed call graph.
//
// Cross-action-group calls are resolved by walking each group's
// actions; non-call actions are validated via the standard registry
// path. The returned map carries every group keyed by name; callers
// hand it to the runtime alongside the per-entity CompiledAutomations.
func CompileActionGroups(groups []ActionGroup, actions *Registry) (CompiledActionGroups, error) {
	// 1. Build the by-name index + reject duplicates.
	byName := make(map[string]ActionGroup, len(groups))
	for _, g := range groups {
		if _, exists := byName[g.Name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrActionGroupDuplicate, g.Name)
		}
		byName[g.Name] = g
	}

	// 2. Build the directed call graph + reject unknown targets.
	// callees[name] = set of names that `name` calls (deduped).
	callees := make(map[string]map[string]struct{}, len(groups))
	for _, g := range groups {
		set := make(map[string]struct{})
		for i, a := range g.Actions {
			if a.Kind != string(ActionCallActionGroup) {
				continue
			}
			var cfg CallActionGroupConfig
			if err := json.Unmarshal(a.Config, &cfg); err != nil {
				return nil, fmt.Errorf("group %q action[%d]: decode call: %w", g.Name, i, err)
			}
			if _, ok := byName[cfg.Name]; !ok {
				return nil, fmt.Errorf("%w: group %q calls %q", ErrActionGroupUnknown, g.Name, cfg.Name)
			}
			set[cfg.Name] = struct{}{}
		}
		callees[g.Name] = set
	}

	// 3. Cycle detection: 3-color DFS. White = unseen, Gray = on the
	// current DFS stack, Black = fully explored. A back-edge to a
	// gray node is a cycle.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(byName))
	var stack []string // names currently on the DFS stack, for the error message

	var visit func(name string) error
	visit = func(name string) error {
		switch color[name] {
		case gray:
			// Find where the cycle starts in the stack so the error
			// quotes only the cycle, not the whole call chain that led
			// to it.
			start := 0
			for i, s := range stack {
				if s == name {
					start = i
					break
				}
			}
			cycle := append([]string{}, stack[start:]...)
			cycle = append(cycle, name)
			return fmt.Errorf("%w: %v", ErrActionGroupCycle, cycle)
		case black:
			return nil
		}
		color[name] = gray
		stack = append(stack, name)
		for callee := range callees[name] {
			if err := visit(callee); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[name] = black
		return nil
	}
	for name := range byName {
		if err := visit(name); err != nil {
			return nil, err
		}
	}

	// 4. Compile each group's actions through the registry. Calls
	// pass through verbatim -- the runtime resolves by name.
	out := make(CompiledActionGroups, len(byName))
	for name, g := range byName {
		compiled, err := compileActions(g.Actions, actions)
		if err != nil {
			return nil, fmt.Errorf("group %q: %w", name, err)
		}
		out[name] = CompiledActionGroup{Name: name, Actions: compiled}
	}
	return out, nil
}

// compileActions decodes one slice of ActionNodes via the registry.
// Lifted out of compileOne so both per-automation actions and per-
// group actions share the path.
func compileActions(nodes []ActionNode, actions *Registry) ([]CompiledAction, error) {
	out := make([]CompiledAction, 0, len(nodes))
	for i, ac := range nodes {
		adef, ok := actions.Get(ac.Kind)
		if !ok {
			return nil, fmt.Errorf("%w: action[%d] %q", ErrUnknownKind, i, ac.Kind)
		}
		acfg, err := adef.Decode(ac.Config)
		if err != nil {
			return nil, fmt.Errorf("decode action[%d] %q: %w", i, ac.Kind, err)
		}
		out = append(out, CompiledAction{Kind: ac.Kind, Config: acfg})
	}
	return out, nil
}
