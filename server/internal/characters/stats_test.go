// Boxland — characters: stat-formula evaluator + point-buy tests.
// Pure tests; no DB.

package characters_test

import (
	"strings"
	"testing"

	"boxland/server/internal/characters"
)

// ---------------------------------------------------------------------------
// EvalFormula
// ---------------------------------------------------------------------------

func TestEvalFormula_Numbers(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"42", 42},
		{"1 + 2", 3},
		{"7 - 3", 4},
		{"2 * 3", 6},
		{"10 / 3", 3},                 // integer truncation
		{"-7", -7},                    // unary minus
		{"+5", 5},                     // unary plus
		{"2 + 3 * 4", 14},             // precedence
		{"(2 + 3) * 4", 20},           // parens
		{"10 - 2 - 3", 5},             // left-associative
		{"2 * 3 + 4 * 5", 26},
		{"-(1 + 2)", -3},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := characters.EvalFormula(tc.in, nil)
			if err != nil {
				t.Fatalf("EvalFormula(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("EvalFormula(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestEvalFormula_StatRefs(t *testing.T) {
	scope := map[string]int{"might": 5, "wit": 3, "spirit": 2}
	cases := []struct {
		in   string
		want int
	}{
		{"might", 5},
		{"might + wit", 8},
		{"might * 2", 10},
		{"10 + might * 2", 20},
		{"5 + wit + spirit", 10},
		{"might * 3", 15},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := characters.EvalFormula(tc.in, scope)
			if err != nil {
				t.Fatalf("EvalFormula(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("EvalFormula(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestEvalFormula_Helpers(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"clamp(15, 0, 10)", 10},
		{"clamp(-5, 0, 10)", 0},
		{"clamp(5, 0, 10)", 5},
		{"min(3, 7)", 3},
		{"max(3, 7)", 7},
		{"min(1, 2, 3)", 1},
		{"max(5, 1, 3)", 5},
		{"clamp(might * 2, 0, 9)", 9},
		{"min(might, wit) + max(might, wit)", 8}, // 3 + 5
	}
	scope := map[string]int{"might": 5, "wit": 3}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := characters.EvalFormula(tc.in, scope)
			if err != nil {
				t.Fatalf("EvalFormula(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("EvalFormula(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestEvalFormula_Errors(t *testing.T) {
	cases := []struct {
		in       string
		scope    map[string]int
		wantSubs string
	}{
		{"", nil, "unexpected end"},
		{"1 +", nil, "unexpected end"},
		{"1 + * 2", nil, "unexpected"},
		{"unknownStat", nil, "missing identifier"},
		{"5 / 0", nil, "division by zero"},
		{"clamp(1, 2)", nil, "3 arguments"},
		{"min()", nil, "needs arguments"},
		{"max(1)", nil, "at least 2"},
		{"clamp(5, 10, 1)", nil, "lo > hi"},
		{"foo()", nil, "unexpected"},  // function-call only allowed for clamp/min/max
		{"@bad", nil, "unexpected character"},
		{"(1 + 2", nil, "expected ')'"},
		{"1 1", nil, "unexpected trailing"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := characters.EvalFormula(tc.in, tc.scope)
			if err == nil {
				t.Fatalf("EvalFormula(%q) = nil err, want error containing %q", tc.in, tc.wantSubs)
			}
			if !strings.Contains(err.Error(), tc.wantSubs) {
				t.Errorf("EvalFormula(%q) err = %q, want substr %q", tc.in, err.Error(), tc.wantSubs)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StatDef.Validate
// ---------------------------------------------------------------------------

func TestStatDef_Validate(t *testing.T) {
	good := characters.StatDef{Key: "might", Label: "Might", Kind: characters.StatCore, Default: 0, Min: 0, Max: 10}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}

	derived := characters.StatDef{Key: "hp", Label: "HP", Kind: characters.StatDerived, Formula: "10 + grit * 2"}
	if err := derived.Validate(); err != nil {
		t.Errorf("derived: %v", err)
	}

	cases := []struct {
		name string
		d    characters.StatDef
		want string
	}{
		{"derived no formula", characters.StatDef{Key: "x", Label: "X", Kind: characters.StatDerived}, "derived stats require a formula"},
		{"core with formula", characters.StatDef{Key: "x", Label: "X", Kind: characters.StatCore, Formula: "1+1"}, "only derived stats may have a formula"},
		{"min > max", characters.StatDef{Key: "x", Label: "X", Kind: characters.StatCore, Min: 10, Max: 5}, "min (10) > max"},
		{"bad key", characters.StatDef{Key: "Bad Key", Label: "x", Kind: characters.StatCore}, "must be lowercase"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.d.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want substr %q", err, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateAllocation + ResolveStats
// ---------------------------------------------------------------------------

func makeStatSet() characters.ParsedStatSet {
	return characters.ParsedStatSet{
		Defs: []characters.StatDef{
			{Key: "might", Label: "Might", Kind: characters.StatCore, Default: 1, Min: 1, Max: 10, CreationCost: 1, DisplayOrder: 1},
			{Key: "wit", Label: "Wit", Kind: characters.StatCore, Default: 1, Min: 1, Max: 10, CreationCost: 1, DisplayOrder: 2},
			{Key: "grit", Label: "Grit", Kind: characters.StatCore, Default: 1, Min: 1, Max: 10, CreationCost: 1, DisplayOrder: 3},
			{Key: "hp", Label: "HP", Kind: characters.StatDerived, Default: 0, Formula: "10 + grit * 2", DisplayOrder: 4},
			{Key: "carry", Label: "Carry", Kind: characters.StatDerived, Default: 0, Formula: "might * 3", DisplayOrder: 5},
			{Key: "stamina", Label: "Stamina", Kind: characters.StatResource, Default: 5, DisplayOrder: 6},
		},
		Rules: characters.CreationRules{Method: "point_buy", Pool: 6},
	}
}

func TestValidateAllocation_PointBuy_HappyPath(t *testing.T) {
	set := makeStatSet()
	alloc := map[string]int{"might": 3, "wit": 2, "grit": 1} // 3+2+1 = 6 == pool
	if err := characters.ValidateAllocation(set, alloc); err != nil {
		t.Errorf("happy: %v", err)
	}
}

func TestValidateAllocation_RejectsMisspentPool(t *testing.T) {
	set := makeStatSet()
	alloc := map[string]int{"might": 3, "wit": 2} // 5 != 6
	err := characters.ValidateAllocation(set, alloc)
	if err == nil || !strings.Contains(err.Error(), "spent 5 points") {
		t.Errorf("got %v, want pool mismatch", err)
	}
}

func TestValidateAllocation_RejectsAboveMax(t *testing.T) {
	set := makeStatSet()
	// might default=1, max=10 -> +20 would push it to 21
	alloc := map[string]int{"might": 20}
	err := characters.ValidateAllocation(set, alloc)
	if err == nil || !strings.Contains(err.Error(), "above max") {
		t.Errorf("got %v, want above max", err)
	}
}

func TestValidateAllocation_RejectsBelowMin(t *testing.T) {
	set := makeStatSet()
	// might default=1, min=1 -> -5 would push it to -4
	alloc := map[string]int{"might": -5}
	err := characters.ValidateAllocation(set, alloc)
	if err == nil || !strings.Contains(err.Error(), "below min") {
		t.Errorf("got %v, want below min", err)
	}
}

func TestValidateAllocation_RejectsUnknownKey(t *testing.T) {
	set := makeStatSet()
	alloc := map[string]int{"unknown": 1}
	err := characters.ValidateAllocation(set, alloc)
	if err == nil || !strings.Contains(err.Error(), "unknown stat key") {
		t.Errorf("got %v, want unknown key", err)
	}
}

func TestValidateAllocation_RejectsAllocationToDerived(t *testing.T) {
	set := makeStatSet()
	alloc := map[string]int{"hp": 5}
	err := characters.ValidateAllocation(set, alloc)
	if err == nil || !strings.Contains(err.Error(), "only core stats") {
		t.Errorf("got %v, want core-only error", err)
	}
}

func TestValidateAllocation_FixedRejectsAnyAlloc(t *testing.T) {
	set := makeStatSet()
	set.Rules = characters.CreationRules{Method: "fixed"}
	alloc := map[string]int{"might": 1}
	err := characters.ValidateAllocation(set, alloc)
	if err == nil || !strings.Contains(err.Error(), "fixed creation rejects") {
		t.Errorf("got %v, want fixed reject", err)
	}
}

func TestValidateAllocation_FreeformAcceptsArbitrary(t *testing.T) {
	set := makeStatSet()
	set.Rules = characters.CreationRules{Method: "freeform"}
	alloc := map[string]int{"might": 9, "wit": 9, "grit": 9}
	if err := characters.ValidateAllocation(set, alloc); err != nil {
		t.Errorf("freeform: %v", err)
	}
}

func TestResolveStats_ComputesDerivedAndCaps(t *testing.T) {
	set := makeStatSet()
	cap := 20
	for i := range set.Defs {
		if set.Defs[i].Key == "carry" {
			set.Defs[i].Cap = &cap
		}
	}
	alloc := map[string]int{"might": 3, "wit": 2, "grit": 1} // might=4, grit=2, wit=3
	got, err := characters.ResolveStats(set, alloc)
	if err != nil {
		t.Fatalf("ResolveStats: %v", err)
	}
	want := map[string]int{
		"might":   4,
		"wit":     3,
		"grit":    2,
		"hp":      14, // 10 + 2*2
		"carry":   12, // 4 * 3
		"stamina": 5,  // resource default
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	// Now bump might high enough that carry would exceed the cap.
	alloc = map[string]int{"might": 6, "wit": 0, "grit": 0} // might=7
	got, err = characters.ResolveStats(set, alloc)
	if err != nil {
		t.Fatalf("ResolveStats (cap): %v", err)
	}
	if got["carry"] != 20 { // 7*3=21, capped at 20
		t.Errorf("carry = %d, want 20 (capped)", got["carry"])
	}
}

func TestResolveStats_ResolvesDerivedOfDerived(t *testing.T) {
	// Derived A depends on derived B (and B depends on a core stat).
	// The two-pass loop should resolve them in either declaration order.
	set := characters.ParsedStatSet{
		Defs: []characters.StatDef{
			{Key: "a", Label: "A", Kind: characters.StatDerived, Formula: "b * 2", DisplayOrder: 1},
			{Key: "b", Label: "B", Kind: characters.StatDerived, Formula: "core + 1", DisplayOrder: 2},
			{Key: "core", Label: "Core", Kind: characters.StatCore, Default: 5, Min: 0, Max: 10, DisplayOrder: 3},
		},
		Rules: characters.CreationRules{Method: "fixed"},
	}
	got, err := characters.ResolveStats(set, nil)
	if err != nil {
		t.Fatalf("ResolveStats: %v", err)
	}
	if got["b"] != 6 || got["a"] != 12 {
		t.Errorf("derived chain: a=%d b=%d, want a=12 b=6", got["a"], got["b"])
	}
}

func TestResolveStats_RejectsCycle(t *testing.T) {
	set := characters.ParsedStatSet{
		Defs: []characters.StatDef{
			{Key: "a", Label: "A", Kind: characters.StatDerived, Formula: "b + 1"},
			{Key: "b", Label: "B", Kind: characters.StatDerived, Formula: "a + 1"},
		},
		Rules: characters.CreationRules{Method: "fixed"},
	}
	_, err := characters.ResolveStats(set, nil)
	if err == nil || !strings.Contains(err.Error(), "missing dependency or cycle") {
		t.Errorf("got %v, want cycle error", err)
	}
}

// ---------------------------------------------------------------------------
// SortedStatDefs
// ---------------------------------------------------------------------------

func TestSortedStatDefs(t *testing.T) {
	set := makeStatSet()
	got := characters.SortedStatDefs(set)
	for i := 1; i < len(got); i++ {
		if got[i-1].DisplayOrder > got[i].DisplayOrder {
			t.Errorf("not sorted at %d: %d > %d", i, got[i-1].DisplayOrder, got[i].DisplayOrder)
		}
	}
}
