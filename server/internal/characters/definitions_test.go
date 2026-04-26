// Boxland — characters: row-type Validate() unit tests. Pure tests; no DB.

package characters_test

import (
	"encoding/json"
	"strings"
	"testing"

	"boxland/server/internal/characters"
)

func TestSlot_Validate(t *testing.T) {
	good := characters.Slot{Key: "body", Label: "Body", Required: true}
	if err := good.Validate(); err != nil {
		t.Fatalf("good slot: %v", err)
	}

	bad := []struct {
		name string
		s    characters.Slot
		want string
	}{
		{"empty key", characters.Slot{Key: "", Label: "x"}, "key is required"},
		{"upper key", characters.Slot{Key: "Body", Label: "x"}, "must be lowercase"},
		{"hyphen key", characters.Slot{Key: "hair-front", Label: "x"}, "must be lowercase"},
		{"space label", characters.Slot{Key: "body", Label: "  "}, "label is required"},
		{"long label", characters.Slot{Key: "body", Label: strings.Repeat("x", characters.MaxNameLen+1)}, "exceeds"},
		{"long key", characters.Slot{Key: strings.Repeat("a", 33), Label: "x"}, "exceeds 32"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestPart_Validate(t *testing.T) {
	good := characters.Part{
		SlotID: 1, AssetID: 2, Name: "Plain Body",
		Tags: []string{"npc", "human"},
		FrameMapJSON: json.RawMessage(`{"idle":[0,0]}`),
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good part: %v", err)
	}

	cases := []struct {
		name string
		p    characters.Part
		want string
	}{
		{"missing slot", characters.Part{AssetID: 2, Name: "x"}, "slot_id is required"},
		{"missing asset", characters.Part{SlotID: 1, Name: "x"}, "asset_id is required"},
		{"empty name", characters.Part{SlotID: 1, AssetID: 2, Name: " "}, "name is required"},
		{"empty tag", characters.Part{SlotID: 1, AssetID: 2, Name: "x", Tags: []string{""}}, "tags must not contain empty"},
		{"bad json", characters.Part{SlotID: 1, AssetID: 2, Name: "x", FrameMapJSON: json.RawMessage(`{not json`)}, "valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestRecipe_Validate(t *testing.T) {
	good := characters.Recipe{
		OwnerKind: characters.OwnerKindDesigner,
		OwnerID:   1, Name: "Test",
		RecipeHash: []byte("hash"),
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}

	cases := []struct {
		name string
		r    characters.Recipe
		want string
	}{
		{"bad owner_kind", characters.Recipe{OwnerKind: "world", OwnerID: 1, Name: "x", RecipeHash: []byte("h")}, "owner_kind"},
		{"missing owner_id", characters.Recipe{OwnerKind: "designer", Name: "x", RecipeHash: []byte("h")}, "owner_id is required"},
		{"missing hash", characters.Recipe{OwnerKind: "designer", OwnerID: 1, Name: "x"}, "recipe_hash is required"},
		{
			"oversize payload",
			characters.Recipe{
				OwnerKind: "designer", OwnerID: 1, Name: "x", RecipeHash: []byte("h"),
				AppearanceJSON: json.RawMessage(strings.Repeat("a", characters.MaxRecipeJSONBytes+1)),
			},
			"exceeds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestBake_Validate(t *testing.T) {
	assetID := int64(99)
	good := characters.Bake{
		RecipeID: 1, RecipeHash: []byte("h"),
		Status: characters.BakeStatusBaked, AssetID: &assetID,
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}

	cases := []struct {
		name string
		b    characters.Bake
		want string
	}{
		{"missing recipe", characters.Bake{RecipeHash: []byte("h"), Status: characters.BakeStatusPending}, "recipe_id is required"},
		{"bad status", characters.Bake{RecipeID: 1, RecipeHash: []byte("h"), Status: "weird"}, "status"},
		{"baked without asset", characters.Bake{RecipeID: 1, RecipeHash: []byte("h"), Status: characters.BakeStatusBaked}, "must reference an asset_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.b.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestStatSet_Validate(t *testing.T) {
	good := characters.StatSet{Key: "default", Name: "Default"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	if err := (characters.StatSet{Key: "", Name: "x"}).Validate(); err == nil {
		t.Errorf("expected key validation error")
	}
}

func TestTalentTree_Validate(t *testing.T) {
	good := characters.TalentTree{Key: "warrior", Name: "Warrior", LayoutMode: characters.LayoutTree, CurrencyKey: "talent_points"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}

	cases := []struct {
		name string
		t    characters.TalentTree
		want string
	}{
		{"bad layout", characters.TalentTree{Key: "k", Name: "n", LayoutMode: "circle", CurrencyKey: "tp"}, "layout_mode"},
		{"bad currency", characters.TalentTree{Key: "k", Name: "n", LayoutMode: "tree", CurrencyKey: "Talent Points"}, "currency_key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.t.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestTalentNode_Validate(t *testing.T) {
	good := characters.TalentNode{TreeID: 1, Key: "fireball", Name: "Fireball", MaxRank: 3, MutexGroup: "fire_school"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}

	cases := []struct {
		name string
		n    characters.TalentNode
		want string
	}{
		{"missing tree", characters.TalentNode{Key: "k", Name: "n", MaxRank: 1}, "tree_id is required"},
		{"zero rank", characters.TalentNode{TreeID: 1, Key: "k", Name: "n", MaxRank: 0}, "max_rank"},
		{"bad mutex", characters.TalentNode{TreeID: 1, Key: "k", Name: "n", MaxRank: 1, MutexGroup: "Fire School"}, "mutex_group"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.n.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestNpcTemplate_Validate(t *testing.T) {
	good := characters.NpcTemplate{Name: "Goblin", Tags: []string{"npc", "hostile"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	bad := characters.NpcTemplate{Name: "Goblin", Tags: []string{""}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "tags must not") {
		t.Errorf("expected tag-validation error, got %v", err)
	}
}

func TestPlayerCharacter_Validate(t *testing.T) {
	good := characters.PlayerCharacter{PlayerID: 1, Name: "Aria"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}

	cases := []struct {
		name string
		p    characters.PlayerCharacter
		want string
	}{
		{"missing player", characters.PlayerCharacter{Name: "x"}, "player_id is required"},
		{"empty name", characters.PlayerCharacter{PlayerID: 1, Name: " "}, "name is required"},
		{"long bio", characters.PlayerCharacter{PlayerID: 1, Name: "x", PublicBio: strings.Repeat("a", characters.MaxBioLen+1)}, "public_bio exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, err.Error())
			}
		})
	}
}
