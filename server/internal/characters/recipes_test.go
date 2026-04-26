// Boxland — characters: recipe normalization + hashing tests.
//
// Pure, no DB. Covers the dedup invariants the bake pipeline depends
// on: permutation-stable hashing, meaningful-change-sensitive hashing,
// and rejection of malformed input.

package characters_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"boxland/server/internal/characters"
)

// helper: marshal v or fail the test
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestNormalizeRecipe_PermutationStable_Slots(t *testing.T) {
	a := mustJSON(t, characters.AppearanceSelection{
		Slots: []characters.AppearanceSlot{
			{SlotKey: "body", PartID: 1},
			{SlotKey: "hair_front", PartID: 2},
			{SlotKey: "boots", PartID: 3},
		},
	})
	b := mustJSON(t, characters.AppearanceSelection{
		Slots: []characters.AppearanceSlot{
			{SlotKey: "boots", PartID: 3},
			{SlotKey: "body", PartID: 1},
			{SlotKey: "hair_front", PartID: 2},
		},
	})
	canonA, err := characters.NormalizeRecipe("Hero", a, nil, nil)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	canonB, err := characters.NormalizeRecipe("Hero", b, nil, nil)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if !bytes.Equal(canonA, canonB) {
		t.Errorf("permutation produced different canonical bytes:\n  a=%s\n  b=%s", canonA, canonB)
	}
}

func TestNormalizeRecipe_PermutationStable_Palette(t *testing.T) {
	a := mustJSON(t, characters.AppearanceSelection{
		Slots: []characters.AppearanceSlot{
			{SlotKey: "body", PartID: 1, Palette: map[string]string{"primary": "#aabbcc", "accent": "#001122"}},
		},
	})
	b := mustJSON(t, characters.AppearanceSelection{
		Slots: []characters.AppearanceSlot{
			{SlotKey: "body", PartID: 1, Palette: map[string]string{"accent": "#001122", "primary": "#aabbcc"}},
		},
	})
	hashA, _ := characters.ComputeRecipeHash("X", a, nil, nil)
	hashB, _ := characters.ComputeRecipeHash("X", b, nil, nil)
	if !bytes.Equal(hashA, hashB) {
		t.Errorf("palette key reordering changed hash")
	}
}

func TestNormalizeRecipe_PermutationStable_StatsAndTalents(t *testing.T) {
	statsA := mustJSON(t, characters.StatSelection{
		SetID:       4,
		Allocations: map[string]int{"might": 3, "wit": 1, "grit": 2},
	})
	statsB := mustJSON(t, characters.StatSelection{
		SetID:       4,
		Allocations: map[string]int{"wit": 1, "grit": 2, "might": 3},
	})
	talentsA := mustJSON(t, characters.TalentSelection{
		Picks: map[string]int{"warrior.cleave": 1, "warrior.shield_bash": 2},
	})
	talentsB := mustJSON(t, characters.TalentSelection{
		Picks: map[string]int{"warrior.shield_bash": 2, "warrior.cleave": 1},
	})
	hashA, _ := characters.ComputeRecipeHash("X", nil, statsA, talentsA)
	hashB, _ := characters.ComputeRecipeHash("X", nil, statsB, talentsB)
	if !bytes.Equal(hashA, hashB) {
		t.Errorf("map reordering changed hash")
	}
}

func TestNormalizeRecipe_NameAffectsHash(t *testing.T) {
	a := mustJSON(t, characters.AppearanceSelection{Slots: []characters.AppearanceSlot{{SlotKey: "body", PartID: 1}}})
	hashA, _ := characters.ComputeRecipeHash("Aria", a, nil, nil)
	hashB, _ := characters.ComputeRecipeHash("Bree", a, nil, nil)
	if bytes.Equal(hashA, hashB) {
		t.Errorf("name change should change hash")
	}
}

func TestNormalizeRecipe_PartChangeAffectsHash(t *testing.T) {
	a := mustJSON(t, characters.AppearanceSelection{Slots: []characters.AppearanceSlot{{SlotKey: "body", PartID: 1}}})
	b := mustJSON(t, characters.AppearanceSelection{Slots: []characters.AppearanceSlot{{SlotKey: "body", PartID: 2}}})
	hashA, _ := characters.ComputeRecipeHash("X", a, nil, nil)
	hashB, _ := characters.ComputeRecipeHash("X", b, nil, nil)
	if bytes.Equal(hashA, hashB) {
		t.Errorf("changing PartID should change hash")
	}
}

func TestNormalizeRecipe_LayerOverrideAffectsHash(t *testing.T) {
	override := int32(150)
	a := mustJSON(t, characters.AppearanceSelection{Slots: []characters.AppearanceSlot{{SlotKey: "body", PartID: 1}}})
	b := mustJSON(t, characters.AppearanceSelection{Slots: []characters.AppearanceSlot{{SlotKey: "body", PartID: 1, LayerOrder: &override}}})
	hashA, _ := characters.ComputeRecipeHash("X", a, nil, nil)
	hashB, _ := characters.ComputeRecipeHash("X", b, nil, nil)
	if bytes.Equal(hashA, hashB) {
		t.Errorf("LayerOrder override should change hash")
	}
}

func TestNormalizeRecipe_EmptyAndNilCollapse(t *testing.T) {
	// nil, empty bytes, "null" should all canonicalize to the same
	// minimal envelope.
	hashes := [][]byte{}
	for _, raw := range [][]byte{nil, {}, []byte("null"), []byte(`  `)} {
		h, err := characters.ComputeRecipeHash("X", raw, raw, raw)
		if err != nil {
			t.Fatalf("raw=%q: %v", raw, err)
		}
		hashes = append(hashes, h)
	}
	for i := 1; i < len(hashes); i++ {
		if !bytes.Equal(hashes[0], hashes[i]) {
			t.Errorf("empty input %d hashed differently from nil", i)
		}
	}
}

func TestNormalizeRecipe_RejectsDuplicateSlot(t *testing.T) {
	bad := []byte(`{"slots":[{"slot_key":"body","part_id":1},{"slot_key":"body","part_id":2}]}`)
	_, err := characters.NormalizeRecipe("X", bad, nil, nil)
	if err == nil {
		t.Fatal("expected duplicate slot rejection, got nil")
	}
	if !strings.Contains(err.Error(), "appears more than once") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNormalizeRecipe_RejectsEmptySlotKey(t *testing.T) {
	bad := []byte(`{"slots":[{"slot_key":"","part_id":1}]}`)
	_, err := characters.NormalizeRecipe("X", bad, nil, nil)
	if err == nil {
		t.Fatal("expected empty slot_key rejection")
	}
	if !strings.Contains(err.Error(), "slot_key is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNormalizeRecipe_RejectsBadJSON(t *testing.T) {
	_, err := characters.NormalizeRecipe("X", []byte(`{not json`), nil, nil)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestNormalizeRecipe_DeterministicAcrossCalls(t *testing.T) {
	a := mustJSON(t, characters.AppearanceSelection{
		Slots: []characters.AppearanceSlot{{SlotKey: "body", PartID: 1, Palette: map[string]string{"a": "1", "b": "2"}}},
	})
	first, _ := characters.NormalizeRecipe("X", a, nil, nil)
	for i := 0; i < 100; i++ {
		again, err := characters.NormalizeRecipe("X", a, nil, nil)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("non-deterministic at iter %d", i)
		}
	}
}

func TestComputeRecipeHash_Length(t *testing.T) {
	h, err := characters.ComputeRecipeHash("X", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 32 {
		t.Errorf("hash length = %d, want 32 (sha256)", len(h))
	}
}

func TestComputeRecipeHash_StableAcrossDBRoundTrip(t *testing.T) {
	// Regression test for Phase 5 audit risk #21: PostgreSQL JSONB
	// stores keys alphabetically, but Go's json.Marshal writes struct
	// fields in declaration order. Without explicit re-canonicalization,
	// the same logical content hashes to different bytes before vs
	// after a JSONB round-trip, breaking dedup.
	//
	// This test simulates the round-trip by going:
	//   typed struct -> json.Marshal -> json.Unmarshal to map ->
	//     json.Marshal (alphabetical) -> hash
	// vs:
	//   typed struct -> json.Marshal -> hash directly.
	// Both must produce the same hash.

	original := mustJSON(t, characters.AppearanceSelection{
		Slots: []characters.AppearanceSlot{
			{SlotKey: "body", PartID: 1},
			{SlotKey: "hair_front", PartID: 2},
		},
	})

	// Simulate what JSONB does: parse then re-marshal alphabetically.
	var generic any
	if err := json.Unmarshal(original, &generic); err != nil {
		t.Fatal(err)
	}
	roundtripped, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}

	hashOriginal, _ := characters.ComputeRecipeHash("X", original, nil, nil)
	hashRoundtripped, _ := characters.ComputeRecipeHash("X", roundtripped, nil, nil)

	if !bytes.Equal(hashOriginal, hashRoundtripped) {
		t.Errorf("hash drifted across DB round-trip: original=%x roundtripped=%x", hashOriginal, hashRoundtripped)
	}
}
