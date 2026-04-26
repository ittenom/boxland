// Boxland — characters: cross-entity validation.
//
// One source of truth for "is this recipe + stat set + talent tree
// combination valid right now". Runs at publish time (inside the
// publish tx so the validator sees the same view as the bake) and is
// also exposed as a service helper for the Generator UI's preview
// validation surface.

package characters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// validateRecipeStatsAndTalents is called from
// NpcTemplateHandler.Publish before the (expensive) bake. Best-effort:
// if the recipe doesn't reference a stat set, stat allocation is
// skipped; if it doesn't reference any talent trees, talent validation
// is skipped. Anything that IS referenced must resolve cleanly.
func validateRecipeStatsAndTalents(ctx context.Context, tx pgx.Tx, recipe BakeRecipe) error {
	statScope, err := validateStatsForRecipe(ctx, tx, recipe.StatsJSON)
	if err != nil {
		return err
	}
	if err := validateTalentsForRecipe(ctx, tx, recipe.TalentsJSON, statScope); err != nil {
		return err
	}
	return nil
}

// validateStatsForRecipe parses the recipe's stats_json, loads the
// referenced stat set (if any), validates the allocation, and returns
// the resolved scope (so the talent validator can compute budgets).
//
// Returns an empty scope when the recipe has no stats; the talent
// validator treats that as "no currency available".
func validateStatsForRecipe(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (map[string]int, error) {
	if len(raw) == 0 {
		return map[string]int{}, nil
	}
	var sel StatSelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return nil, fmt.Errorf("stats_json: %w", err)
	}
	if sel.SetID == 0 {
		// Allocation without a referenced set is a designer mistake;
		// surface it loudly so they don't end up baking a recipe that
		// silently ignored their stat picks.
		if len(sel.Allocations) > 0 {
			return nil, errors.New("stats: allocations supplied but stats.set_id is 0")
		}
		return map[string]int{}, nil
	}
	row, err := loadStatSetRow(ctx, tx, sel.SetID)
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	parsed, err := ParseStatSet(row)
	if err != nil {
		return nil, fmt.Errorf("stats set %d: %w", sel.SetID, err)
	}
	scope, err := ResolveStats(parsed, sel.Allocations)
	if err != nil {
		return nil, fmt.Errorf("stats set %d: %w", sel.SetID, err)
	}
	return scope, nil
}

// validateTalentsForRecipe parses the recipe's talents_json, loads
// every referenced tree (one query per tree key — talent counts are
// small), and runs ValidateTalentSelection against each tree's pick
// subset.
func validateTalentsForRecipe(ctx context.Context, tx pgx.Tx, raw json.RawMessage, statScope map[string]int) error {
	if len(raw) == 0 {
		return nil
	}
	var sel TalentSelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return fmt.Errorf("talents_json: %w", err)
	}
	if len(sel.Picks) == 0 {
		return nil
	}
	grouped := ParsePicks(sel)
	for treeKey, picks := range grouped {
		tree, nodes, err := loadTalentTree(ctx, tx, treeKey)
		if err != nil {
			return fmt.Errorf("talent tree %q: %w", treeKey, err)
		}
		parsed, err := ParseTalentTree(tree, nodes)
		if err != nil {
			return fmt.Errorf("talent tree %q: %w", treeKey, err)
		}
		budget := TalentBudgetFromStats(parsed, statScope)
		if err := ValidateTalentSelection(parsed, picks, budget); err != nil {
			return fmt.Errorf("talent tree %q: %w", treeKey, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// In-tx loaders
// ---------------------------------------------------------------------------

// loadStatSetRow fetches one character_stat_sets row inside the supplied tx.
func loadStatSetRow(ctx context.Context, tx pgx.Tx, id int64) (StatSet, error) {
	var s StatSet
	err := tx.QueryRow(ctx, `
		SELECT id, key, name, stats_json, creation_rules_json, created_by, created_at, updated_at
		FROM character_stat_sets WHERE id = $1
	`, id).Scan(&s.ID, &s.Key, &s.Name, &s.StatsJSON, &s.CreationRulesJSON,
		&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s, ErrStatSetNotFound
		}
		return s, err
	}
	return s, nil
}

// loadTalentTree fetches a tree by key + every node that belongs to
// it. One query each — talents per tree are bounded (typical < 50).
func loadTalentTree(ctx context.Context, tx pgx.Tx, key string) (TalentTree, []TalentNode, error) {
	var tree TalentTree
	err := tx.QueryRow(ctx, `
		SELECT id, key, name, description, currency_key, layout_mode, created_by, created_at, updated_at
		FROM character_talent_trees WHERE key = $1
	`, key).Scan(&tree.ID, &tree.Key, &tree.Name, &tree.Description, &tree.CurrencyKey,
		&tree.LayoutMode, &tree.CreatedBy, &tree.CreatedAt, &tree.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tree, nil, ErrTalentTreeNotFound
		}
		return tree, nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, tree_id, key, name, description, icon_asset_id, max_rank,
		       cost_json, prerequisites_json, effect_json, layout_json, mutex_group,
		       created_at, updated_at
		FROM character_talent_nodes WHERE tree_id = $1 ORDER BY key
	`, tree.ID)
	if err != nil {
		return tree, nil, err
	}
	defer rows.Close()
	var nodes []TalentNode
	for rows.Next() {
		var n TalentNode
		if err := rows.Scan(&n.ID, &n.TreeID, &n.Key, &n.Name, &n.Description,
			&n.IconAssetID, &n.MaxRank, &n.CostJSON, &n.PrerequisitesJSON, &n.EffectJSON,
			&n.LayoutJSON, &n.MutexGroup, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return tree, nil, err
		}
		nodes = append(nodes, n)
	}
	return tree, nodes, rows.Err()
}
