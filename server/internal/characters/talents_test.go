// Boxland — characters: talent validation tests. Pure; no DB.

package characters_test

import (
	"encoding/json"
	"strings"
	"testing"

	"boxland/server/internal/characters"
)

// makeTree builds a small warrior-style tree:
//
//   cleave (max 1, cost 1)
//     |
//     v
//   sweep (max 3, cost 1, requires cleave 1)
//
//   shield_bash (max 1, cost 1, mutex_group "weapon")
//   two_hander  (max 1, cost 1, mutex_group "weapon")
//
// All nodes use the talent_points currency.
func makeTree(t *testing.T) characters.ParsedTree {
	t.Helper()
	tree := characters.TalentTree{
		ID: 1, Key: "warrior", Name: "Warrior", CurrencyKey: "talent_points",
		LayoutMode: characters.LayoutTree,
	}
	mustJSON := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	nodes := []characters.TalentNode{
		{
			TreeID: 1, Key: "cleave", Name: "Cleave", MaxRank: 1,
			CostJSON:   mustJSON(map[string]int{"talent_points": 1}),
			EffectJSON: mustJSON([]characters.TalentEffect{{Kind: "stat_mod", Key: "might", Value: json.RawMessage(`1`)}}),
		},
		{
			TreeID: 1, Key: "sweep", Name: "Sweep", MaxRank: 3,
			CostJSON:          mustJSON(map[string]int{"talent_points": 1}),
			PrerequisitesJSON: mustJSON([]characters.TalentPrereq{{NodeKey: "cleave", MinRank: 1}}),
		},
		{
			TreeID: 1, Key: "shield_bash", Name: "Shield Bash", MaxRank: 1,
			CostJSON:   mustJSON(map[string]int{"talent_points": 1}),
			MutexGroup: "weapon",
		},
		{
			TreeID: 1, Key: "two_hander", Name: "Two-Hander", MaxRank: 1,
			CostJSON:   mustJSON(map[string]int{"talent_points": 1}),
			MutexGroup: "weapon",
		},
	}
	parsed, err := characters.ParseTalentTree(tree, nodes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return parsed
}

// ---------------------------------------------------------------------------
// ParseTalentTree
// ---------------------------------------------------------------------------

func TestParseTalentTree_RejectsDuplicateNodeKey(t *testing.T) {
	tree := characters.TalentTree{ID: 1, Key: "k", Name: "n", CurrencyKey: "tp", LayoutMode: characters.LayoutTree}
	nodes := []characters.TalentNode{
		{TreeID: 1, Key: "dup", Name: "A", MaxRank: 1},
		{TreeID: 1, Key: "dup", Name: "B", MaxRank: 1},
	}
	_, err := characters.ParseTalentTree(tree, nodes)
	if err == nil || !strings.Contains(err.Error(), "appears more than once") {
		t.Errorf("got %v, want duplicate error", err)
	}
}

func TestParseTalentTree_RejectsUnknownPrereq(t *testing.T) {
	tree := characters.TalentTree{ID: 1, Key: "k", Name: "n", CurrencyKey: "tp", LayoutMode: characters.LayoutTree}
	prereqs, _ := json.Marshal([]characters.TalentPrereq{{NodeKey: "ghost", MinRank: 1}})
	nodes := []characters.TalentNode{
		{TreeID: 1, Key: "real", Name: "Real", MaxRank: 1, PrerequisitesJSON: prereqs},
	}
	_, err := characters.ParseTalentTree(tree, nodes)
	if err == nil || !strings.Contains(err.Error(), "unknown node") {
		t.Errorf("got %v, want unknown node error", err)
	}
}

func TestParseTalentTree_RejectsCycles(t *testing.T) {
	tree := characters.TalentTree{ID: 1, Key: "k", Name: "n", CurrencyKey: "tp", LayoutMode: characters.LayoutTree}
	prereqAToB, _ := json.Marshal([]characters.TalentPrereq{{NodeKey: "b", MinRank: 1}})
	prereqBToA, _ := json.Marshal([]characters.TalentPrereq{{NodeKey: "a", MinRank: 1}})
	nodes := []characters.TalentNode{
		{TreeID: 1, Key: "a", Name: "A", MaxRank: 1, PrerequisitesJSON: prereqAToB},
		{TreeID: 1, Key: "b", Name: "B", MaxRank: 1, PrerequisitesJSON: prereqBToA},
	}
	_, err := characters.ParseTalentTree(tree, nodes)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("got %v, want cycle error", err)
	}
}

func TestParseTalentTree_RejectsSelfPrereq(t *testing.T) {
	tree := characters.TalentTree{ID: 1, Key: "k", Name: "n", CurrencyKey: "tp", LayoutMode: characters.LayoutTree}
	pr, _ := json.Marshal([]characters.TalentPrereq{{NodeKey: "self", MinRank: 1}})
	nodes := []characters.TalentNode{
		{TreeID: 1, Key: "self", Name: "S", MaxRank: 1, PrerequisitesJSON: pr},
	}
	_, err := characters.ParseTalentTree(tree, nodes)
	if err == nil || !strings.Contains(err.Error(), "references itself") {
		t.Errorf("got %v, want self-prereq error", err)
	}
}

func TestParseTalentTree_RejectsBadEffectKind(t *testing.T) {
	tree := characters.TalentTree{ID: 1, Key: "k", Name: "n", CurrencyKey: "tp", LayoutMode: characters.LayoutTree}
	eff, _ := json.Marshal([]characters.TalentEffect{{Kind: "obliterate"}})
	nodes := []characters.TalentNode{
		{TreeID: 1, Key: "x", Name: "x", MaxRank: 1, EffectJSON: eff},
	}
	_, err := characters.ParseTalentTree(tree, nodes)
	if err == nil || !strings.Contains(err.Error(), "obliterate") {
		t.Errorf("got %v, want bad-kind error", err)
	}
}

// ---------------------------------------------------------------------------
// ValidateTalentSelection
// ---------------------------------------------------------------------------

func TestValidateTalentSelection_HappyPath(t *testing.T) {
	tree := makeTree(t)
	picks := map[string]int{"cleave": 1, "sweep": 2}
	budget := map[string]int{"talent_points": 5}
	if err := characters.ValidateTalentSelection(tree, picks, budget); err != nil {
		t.Errorf("happy: %v", err)
	}
}

func TestValidateTalentSelection_UnknownNode(t *testing.T) {
	tree := makeTree(t)
	picks := map[string]int{"ghost": 1}
	err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 5})
	if err == nil || !strings.Contains(err.Error(), "not in tree") {
		t.Errorf("got %v, want not-in-tree", err)
	}
}

func TestValidateTalentSelection_OverMaxRank(t *testing.T) {
	tree := makeTree(t)
	picks := map[string]int{"cleave": 1, "sweep": 4} // max_rank=3
	err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 9})
	if err == nil || !strings.Contains(err.Error(), "exceeds max_rank") {
		t.Errorf("got %v, want max_rank error", err)
	}
}

func TestValidateTalentSelection_MissingPrereq(t *testing.T) {
	tree := makeTree(t)
	// sweep needs cleave>=1 but cleave isn't picked.
	picks := map[string]int{"sweep": 1}
	err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 5})
	if err == nil || !strings.Contains(err.Error(), "prereq") {
		t.Errorf("got %v, want prereq error", err)
	}
}

func TestValidateTalentSelection_MutexGroup(t *testing.T) {
	tree := makeTree(t)
	picks := map[string]int{"shield_bash": 1, "two_hander": 1}
	err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 5})
	if err == nil || !strings.Contains(err.Error(), "mutex group") {
		t.Errorf("got %v, want mutex error", err)
	}
}

func TestValidateTalentSelection_ZeroRankIsNoOp(t *testing.T) {
	tree := makeTree(t)
	// Explicit 0 ranks should be ignored — same as not picking.
	picks := map[string]int{"shield_bash": 0, "two_hander": 1}
	if err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 5}); err != nil {
		t.Errorf("zero-rank should be no-op: %v", err)
	}
}

func TestValidateTalentSelection_BudgetEnforced(t *testing.T) {
	tree := makeTree(t)
	picks := map[string]int{"cleave": 1, "sweep": 3} // 1*1 + 1*3 = 4 points
	err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 3})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Errorf("got %v, want budget error", err)
	}
	// Same picks with adequate budget pass.
	if err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 4}); err != nil {
		t.Errorf("exact-budget should pass: %v", err)
	}
}

func TestValidateTalentSelection_NegativeRankRejected(t *testing.T) {
	tree := makeTree(t)
	picks := map[string]int{"cleave": -1}
	err := characters.ValidateTalentSelection(tree, picks, map[string]int{"talent_points": 5})
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Errorf("got %v, want negative error", err)
	}
}

// ---------------------------------------------------------------------------
// ParsePicks + TalentBudgetFromStats
// ---------------------------------------------------------------------------

func TestParsePicks_GroupsByTreeKey(t *testing.T) {
	sel := characters.TalentSelection{
		Picks: map[string]int{
			"warrior.cleave":   1,
			"warrior.sweep":    2,
			"mage.firebolt":    1,
			"orphan_no_dot":    5, // bad shape; should be skipped
		},
	}
	got := characters.ParsePicks(sel)
	if got["warrior"]["cleave"] != 1 || got["warrior"]["sweep"] != 2 {
		t.Errorf("warrior picks: %+v", got["warrior"])
	}
	if got["mage"]["firebolt"] != 1 {
		t.Errorf("mage picks: %+v", got["mage"])
	}
	// orphan key is filtered.
	for tk := range got {
		if _, bad := got[tk]["orphan_no_dot"]; bad {
			t.Errorf("orphan key leaked into tree %q", tk)
		}
	}
}

func TestTalentBudgetFromStats(t *testing.T) {
	tree := characters.ParsedTree{Tree: characters.TalentTree{CurrencyKey: "talent_points"}}
	scope := map[string]int{"talent_points": 5, "might": 3}
	got := characters.TalentBudgetFromStats(tree, scope)
	if got["talent_points"] != 5 {
		t.Errorf("budget: %+v", got)
	}
	// Missing currency stat -> 0 (so spends are blocked).
	tree2 := characters.ParsedTree{Tree: characters.TalentTree{CurrencyKey: "missing"}}
	got2 := characters.TalentBudgetFromStats(tree2, scope)
	if got2["missing"] != 0 {
		t.Errorf("missing budget: %+v", got2)
	}
}
