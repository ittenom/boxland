// Boxland — characters: talent prerequisite + mutex + cost validation.
//
// A talent tree is a designer-authored graph of nodes. Each node has a
// max_rank, a per-currency cost (cost_json), and an optional list of
// prerequisite (node_key, required_rank) edges. mutex_group groups
// mutually-exclusive nodes — at most one node per non-empty group may
// be ranked.
//
// This file provides:
//   - ParseTalentTree: turn typed TalentTree + []TalentNode into a
//     tested ParsedTree with cycle + ref checks at decode time.
//   - ValidateTalentSelection: given a ParsedTree, a player's
//     {node_key: rank} picks, and a currency budget, return nil if
//     every rule is satisfied.
//
// Pure functions; no DB. Designer-side and player-side validators
// share the implementation so they can never diverge.

package characters

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ---------------------------------------------------------------------------
// Talent JSON shapes
// ---------------------------------------------------------------------------

// TalentCost is one entry in a node's cost_json. Stored as a map
// {currency_key: amount-per-rank}; almost every tree uses one currency
// (e.g. talent_points), but the shape supports multi-currency designs.
type TalentCost map[string]int

// TalentPrereq is one edge in a node's prerequisites_json. A pick of
// the parent node requires the player to also have rank >= MinRank on
// the named node.
type TalentPrereq struct {
	NodeKey string `json:"node_key"`
	MinRank int    `json:"min_rank"`
}

// TalentEffect is a structured-data effect emitted by a ranked node.
// One node can declare multiple effects via effect_json (an array).
// Effect kinds are validated structurally; runtime application lives
// outside this package (Phase 3 surfaces the shape; Phase ? wires it
// into the live ECS).
type TalentEffect struct {
	Kind  string         `json:"kind"`            // stat_mod | resource_max | add_tag | set_flag | unlock_action_key
	Key   string         `json:"key,omitempty"`   // stat key / tag / flag / action_key (kind-dependent)
	Value json.RawMessage `json:"value,omitempty"` // amount (int) for stat_mod/resource_max, ignored otherwise
}

// validKindForEffect reports whether an effect kind is one of the
// supported structured shapes.
func validKindForEffect(k string) bool {
	switch k {
	case "stat_mod", "resource_max", "add_tag", "set_flag", "unlock_action_key":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Parsed tree
// ---------------------------------------------------------------------------

// ParsedTree is the typed view of a talent_tree row + its nodes. Built
// once per validation call (cheap; trees are small) so callers don't
// have to thread node slices around.
type ParsedTree struct {
	Tree  TalentTree
	Nodes []ParsedNode
	byKey map[string]int // node key -> index into Nodes
}

// ParsedNode bundles a node row with its decoded cost/prereqs/effects.
type ParsedNode struct {
	Node    TalentNode
	Cost    TalentCost
	Prereqs []TalentPrereq
	Effects []TalentEffect
}

// ParseTalentTree validates and decodes a tree + its nodes. Catches
// decoder errors, missing prereq references, cycles in the prereq
// graph, and bad effect kinds — all before any selection-time check
// runs. Designer save / publish call this; the runtime never has to.
func ParseTalentTree(tree TalentTree, nodes []TalentNode) (ParsedTree, error) {
	out := ParsedTree{Tree: tree, byKey: make(map[string]int, len(nodes))}
	out.Nodes = make([]ParsedNode, 0, len(nodes))

	// Decode each node.
	for i, n := range nodes {
		if _, dup := out.byKey[n.Key]; dup {
			return out, fmt.Errorf("talent_tree %q: node key %q appears more than once", tree.Key, n.Key)
		}
		out.byKey[n.Key] = i

		var cost TalentCost
		if len(n.CostJSON) > 0 {
			if err := json.Unmarshal(n.CostJSON, &cost); err != nil {
				return out, fmt.Errorf("node %q: cost_json: %w", n.Key, err)
			}
		}
		for currency, amt := range cost {
			if currency == "" {
				return out, fmt.Errorf("node %q: cost has empty currency key", n.Key)
			}
			if amt < 0 {
				return out, fmt.Errorf("node %q: cost for %q is negative", n.Key, currency)
			}
		}

		var prereqs []TalentPrereq
		if len(n.PrerequisitesJSON) > 0 {
			if err := json.Unmarshal(n.PrerequisitesJSON, &prereqs); err != nil {
				return out, fmt.Errorf("node %q: prerequisites_json: %w", n.Key, err)
			}
		}
		for _, p := range prereqs {
			if p.NodeKey == "" {
				return out, fmt.Errorf("node %q: prereq has empty node_key", n.Key)
			}
			if p.NodeKey == n.Key {
				return out, fmt.Errorf("node %q: prereq references itself", n.Key)
			}
			if p.MinRank < 1 {
				return out, fmt.Errorf("node %q: prereq min_rank for %q must be >= 1", n.Key, p.NodeKey)
			}
		}

		var effects []TalentEffect
		if len(n.EffectJSON) > 0 {
			if err := json.Unmarshal(n.EffectJSON, &effects); err != nil {
				return out, fmt.Errorf("node %q: effect_json: %w", n.Key, err)
			}
		}
		for i, eff := range effects {
			if !validKindForEffect(eff.Kind) {
				return out, fmt.Errorf("node %q: effect[%d]: kind %q not one of stat_mod|resource_max|add_tag|set_flag|unlock_action_key", n.Key, i, eff.Kind)
			}
		}

		out.Nodes = append(out.Nodes, ParsedNode{
			Node: n, Cost: cost, Prereqs: prereqs, Effects: effects,
		})
	}

	// Verify every prereq node_key resolves.
	for _, pn := range out.Nodes {
		for _, p := range pn.Prereqs {
			if _, ok := out.byKey[p.NodeKey]; !ok {
				return out, fmt.Errorf("node %q: prereq references unknown node %q", pn.Node.Key, p.NodeKey)
			}
		}
	}

	// Cycle detection via DFS coloring. Trees with cycles are rejected
	// at parse time so neither the validator nor the runtime has to
	// cope with them.
	if err := detectPrereqCycle(out); err != nil {
		return out, err
	}

	return out, nil
}

// FindNode returns the parsed node by key, or false.
func (p ParsedTree) FindNode(key string) (ParsedNode, bool) {
	i, ok := p.byKey[key]
	if !ok {
		return ParsedNode{}, false
	}
	return p.Nodes[i], true
}

// detectPrereqCycle runs a 3-color DFS over the prereq edges. White =
// unseen, Gray = on the current path, Black = fully explored. Hitting
// a Gray node along an edge means there's a cycle.
func detectPrereqCycle(p ParsedTree) error {
	color := make(map[string]int, len(p.Nodes)) // 0=white, 1=gray, 2=black
	var visit func(key string, path []string) error
	visit = func(key string, path []string) error {
		switch color[key] {
		case 1:
			return fmt.Errorf("talent_tree %q: prereq cycle through %v -> %s", p.Tree.Key, path, key)
		case 2:
			return nil
		}
		color[key] = 1
		path = append(path, key)
		idx := p.byKey[key]
		for _, pr := range p.Nodes[idx].Prereqs {
			if err := visit(pr.NodeKey, path); err != nil {
				return err
			}
		}
		color[key] = 2
		return nil
	}
	for _, n := range p.Nodes {
		if color[n.Node.Key] == 0 {
			if err := visit(n.Node.Key, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Selection validation
// ---------------------------------------------------------------------------

// ValidateTalentSelection enforces every selection-time rule a recipe
// must satisfy. picks is the {node_key: rank} map the player chose.
// budget is the available amount of each currency (e.g.
// {"talent_points": 5}); zero/missing entries treat that currency as
// unspendable.
//
// Returns nil if every rule passes.
func ValidateTalentSelection(tree ParsedTree, picks map[string]int, budget map[string]int) error {
	if budget == nil {
		budget = map[string]int{}
	}
	mutexUsed := make(map[string]string) // mutex_group -> first ranked node key
	spend := make(map[string]int)        // currency -> total spend

	// Iterate in sorted order so error messages are deterministic.
	keys := make([]string, 0, len(picks))
	for k := range picks {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		rank := picks[k]
		if rank == 0 {
			continue // explicit zero rank = no pick; skip cleanly
		}
		if rank < 0 {
			return fmt.Errorf("talent %q: rank %d is negative", k, rank)
		}
		node, ok := tree.FindNode(k)
		if !ok {
			return fmt.Errorf("talent %q: not in tree %q", k, tree.Tree.Key)
		}
		if rank > int(node.Node.MaxRank) {
			return fmt.Errorf("talent %q: rank %d exceeds max_rank %d", k, rank, node.Node.MaxRank)
		}
		// Prereqs.
		for _, p := range node.Prereqs {
			have := picks[p.NodeKey]
			if have < p.MinRank {
				return fmt.Errorf("talent %q: prereq %q rank %d (have %d)", k, p.NodeKey, p.MinRank, have)
			}
		}
		// Mutex groups (non-empty).
		if g := node.Node.MutexGroup; g != "" {
			if other, taken := mutexUsed[g]; taken {
				return fmt.Errorf("talent %q: mutex group %q already taken by %q", k, g, other)
			}
			mutexUsed[g] = k
		}
		// Cost.
		for currency, amt := range node.Cost {
			spend[currency] += amt * rank
		}
	}

	// Budget enforcement.
	for currency, amt := range spend {
		if amt > budget[currency] {
			return fmt.Errorf("talent budget for %q exceeded: spent %d, have %d", currency, amt, budget[currency])
		}
	}
	return nil
}

// TalentBudgetFromStats extracts the spendable amount of each currency
// from a resolved stat scope. The tree's currency_key picks the
// matching stat — by convention that stat has Kind=resource. Returns
// {currency_key: stat_value} suitable for ValidateTalentSelection.
//
// If the tree's currency_key is missing from the stat scope, the
// returned budget treats it as 0 (so the picker can't accidentally
// spend points the recipe doesn't have).
func TalentBudgetFromStats(tree ParsedTree, scope map[string]int) map[string]int {
	out := map[string]int{}
	if v, ok := scope[tree.Tree.CurrencyKey]; ok {
		out[tree.Tree.CurrencyKey] = v
	} else {
		out[tree.Tree.CurrencyKey] = 0
	}
	return out
}

// ParsePicks decodes a recipe's TalentSelection into a flat
// {tree_key.node_key: rank} -> {tree_key: {node_key: rank}} grouping.
// The recipe stores picks as one map keyed by "tree.node" so a single
// recipe can span multiple trees; ValidateTalentSelection takes one
// tree at a time, so the caller groups first.
func ParsePicks(sel TalentSelection) map[string]map[string]int {
	out := make(map[string]map[string]int, 4)
	for k, rank := range sel.Picks {
		treeKey, nodeKey := splitDotted(k)
		if treeKey == "" || nodeKey == "" {
			continue
		}
		m, ok := out[treeKey]
		if !ok {
			m = map[string]int{}
			out[treeKey] = m
		}
		m[nodeKey] = rank
	}
	return out
}

// splitDotted splits "tree.node" into (tree, node). Tolerates a missing
// dot by returning two empty strings.
func splitDotted(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i+1:]
		}
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// Designer-side helpers
// ---------------------------------------------------------------------------

// DescribeTalentEffectShape returns a stable descriptor of every
// allowed effect kind. Used by the designer-side editor's effect
// picker to render kind-specific form fields. Kept here so additions
// land alongside the validator (one source of truth).
func DescribeTalentEffectShape() []string {
	return []string{"stat_mod", "resource_max", "add_tag", "set_flag", "unlock_action_key"}
}

// CycleSentinel is the error wrap-friendly token surfaced when a cycle
// is detected. Tests can errors.Is against it; downstream code can use
// it to map to a specific HTTP status.
var CycleSentinel = errors.New("talent_tree: prereq cycle")
