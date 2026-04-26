// Boxland — player-mode character save/load endpoints.
//
// Player flow contrast with the designer flow:
//
//   designer:  Generator -> draft -> Push to Live -> bake on publish
//   player:    Generator -> save -> bake INLINE -> link to character
//
// Players don't go through the publish pipeline because their characters
// belong to them, not to the realm. The bake runs synchronously in the
// save handler so the response carries the new bake's asset id back to
// the UI immediately.
//
// Every route in this file scopes by player_id from PlayerFromContext.
// The request body's player_id (if any) is *ignored* — defense in depth
// against confused-deputy attacks.

package playerweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/characters"
	"boxland/server/views"
)

// playerRecipeBodyMaxBytes caps the JSON body for save endpoints.
// Aligned with characters.MaxRecipeJSONBytes plus envelope.
const playerRecipeBodyMaxBytes = 64 * 1024

// playerCharacterPayload is the shape both the client posts and the
// server returns. Mirrors the designer-mode recipePayload so the
// generator UI module handles both surfaces with one code path.
type playerCharacterPayload struct {
	ID         int64                          `json:"id,omitempty"`            // player_character id
	RecipeID   int64                          `json:"recipe_id,omitempty"`
	Name       string                         `json:"name"`
	PublicBio  string                         `json:"public_bio,omitempty"`
	Appearance characters.AppearanceSelection `json:"appearance"`
	Stats      characters.StatSelection       `json:"stats"`
	Talents    characters.TalentSelection     `json:"talents"`
	BakeID     int64                          `json:"bake_id,omitempty"`       // server fills on response
	BakeAsset  int64                          `json:"bake_asset_id,omitempty"` // server fills on response
}

// readBody decodes the JSON body with a size cap. Surfaces clear errors
// for empty body / bad JSON / oversize.
func readPlayerCharacterBody(r *http.Request) (playerCharacterPayload, error) {
	var out playerCharacterPayload
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, playerRecipeBodyMaxBytes))
	if err != nil {
		return out, fmt.Errorf("body too large or unreadable: %w", err)
	}
	if len(body) == 0 {
		return out, errors.New("body is empty")
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("bad json: %w", err)
	}
	return out, nil
}

// getPlayerCharacterGeneratorPage renders the player-mode generator
// for the "create new" entry point. The character id is 0 in the boot
// data; the JS POSTs to /play/characters on save and gets back the
// new id.
func getPlayerCharacterGeneratorPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, r, views.PlayCharacterGenerator(views.PlayCharacterGeneratorProps{
			CharacterID: 0,
			Name:        "",
			RecipeID:    0,
		}))
	}
}

// getPlayerCharacterEditPage renders the generator pre-loaded with an
// existing character. Cross-player access maps to 404 to avoid leaking
// existence.
func getPlayerCharacterEditPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Characters == nil {
			http.Error(w, "characters unavailable", http.StatusServiceUnavailable)
			return
		}
		p := PlayerFromContext(r.Context())
		if p == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		row, err := d.Characters.FindPlayerCharacter(r.Context(), p.ID, id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		recipeID := int64(0)
		if row.RecipeID != nil {
			recipeID = *row.RecipeID
		}
		render(w, r, views.PlayCharacterGenerator(views.PlayCharacterGeneratorProps{
			CharacterID: row.ID,
			Name:        row.Name,
			RecipeID:    recipeID,
		}))
	}
}

// listPlayerCharacters handles GET /play/characters. Returns every
// character owned by the requesting player.
func listPlayerCharacters(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Characters == nil {
			http.Error(w, "characters unavailable", http.StatusServiceUnavailable)
			return
		}
		p := PlayerFromContext(r.Context())
		if p == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rows, err := d.Characters.ListPlayerCharacters(r.Context(), p.ID)
		if err != nil {
			slog.Error("list player characters", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"characters": rows})
	}
}

// getPlayerCharacter handles GET /play/characters/{id}. Returns the
// character + its recipe payload (so the generator UI can rehydrate).
// Cross-player access maps to 404.
func getPlayerCharacter(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Characters == nil {
			http.Error(w, "characters unavailable", http.StatusServiceUnavailable)
			return
		}
		p := PlayerFromContext(r.Context())
		if p == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		row, err := d.Characters.FindPlayerCharacter(r.Context(), p.ID, id)
		if err != nil {
			// Both NotFound and Forbidden surface as 404 — never leak
			// existence of another player's row to the requester.
			if errors.Is(err, characters.ErrPlayerCharNotFound) || errors.Is(err, characters.ErrForbidden) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("get player character", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out := playerCharacterPayload{
			ID:        row.ID,
			Name:      row.Name,
			PublicBio: row.PublicBio,
		}
		if row.RecipeID != nil {
			out.RecipeID = *row.RecipeID
			recipe, err := d.Characters.FindRecipeByID(r.Context(), *row.RecipeID)
			if err == nil && recipe.OwnerKind == characters.OwnerKindPlayer && recipe.OwnerID == p.ID {
				_ = json.Unmarshal(recipe.AppearanceJSON, &out.Appearance)
				_ = json.Unmarshal(recipe.StatsJSON, &out.Stats)
				_ = json.Unmarshal(recipe.TalentsJSON, &out.Talents)
			}
		}
		if row.ActiveBakeID != nil {
			out.BakeID = *row.ActiveBakeID
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// createPlayerCharacter handles POST /play/characters. The shell row,
// recipe, bake, and link all land inside ONE transaction so a bake
// failure rolls everything back — no orphan rows can leak even if the
// server crashes mid-request.
func createPlayerCharacter(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Characters == nil {
			http.Error(w, "characters unavailable", http.StatusServiceUnavailable)
			return
		}
		p := PlayerFromContext(r.Context())
		if p == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		in, err := readPlayerCharacterBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		charID, recipeID, bakeID, bakeAssetID, err := saveNewPlayerCharacterAtomic(r.Context(), d, p.ID, in)
		if err != nil {
			slog.Error("create player character", "err", err)
			http.Error(w, "save failed: "+err.Error(), http.StatusBadRequest)
			return
		}

		out := in
		out.ID = charID
		out.RecipeID = recipeID
		out.BakeID = bakeID
		out.BakeAsset = bakeAssetID
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(out)
	}
}

// saveNewPlayerCharacterAtomic creates the shell row + the recipe row +
// runs the bake + links the character to the recipe/bake — all inside
// one transaction. If any step errors, the entire write set rolls back
// cleanly.
//
// This replaces the prior multi-step path (Create + SavePlayerRecipeAndBake
// + Link) which had a race window between shell create and bake start.
func saveNewPlayerCharacterAtomic(
	ctx context.Context,
	d Deps,
	playerID int64,
	in playerCharacterPayload,
) (charID, recipeID, bakeID, bakeAssetID int64, err error) {
	if d.Characters.Store == nil || d.Characters.Assets == nil {
		return 0, 0, 0, 0, errors.New("bake deps not configured")
	}
	tx, err := d.Characters.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Shell row first so the bake can later UPDATE it with the link.
	if err := tx.QueryRow(ctx, `
		INSERT INTO player_characters (player_id, name, public_bio)
		VALUES ($1, $2, $3) RETURNING id
	`, playerID, in.Name, in.PublicBio).Scan(&charID); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("insert shell: %w", err)
	}

	// Recipe + bake.
	rid, bid, baid, err := insertRecipeAndBakeInTx(ctx, tx, d, playerID, in)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	recipeID, bakeID, bakeAssetID = rid, bid, baid

	// Link the shell to the new recipe + bake.
	if _, err := tx.Exec(ctx, `
		UPDATE player_characters
		SET recipe_id = $1, active_bake_id = $2, updated_at = now()
		WHERE id = $3 AND player_id = $4
	`, recipeID, bakeID, charID, playerID); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("link character: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("commit: %w", err)
	}
	return charID, recipeID, bakeID, bakeAssetID, nil
}

// insertRecipeAndBakeInTx is the shared core used by both
// saveNewPlayerCharacterAtomic and updatePlayerCharacter. Inserts a
// fresh recipe row + runs the bake inside the supplied tx. Returns
// the new recipe id, bake id, and bake asset id.
func insertRecipeAndBakeInTx(
	ctx context.Context,
	tx pgx.Tx,
	d Deps,
	playerID int64,
	in playerCharacterPayload,
) (recipeID, bakeID, bakeAssetID int64, err error) {
	appearanceJSON, _ := json.Marshal(in.Appearance)
	statsJSON, _ := json.Marshal(in.Stats)
	talentsJSON, _ := json.Marshal(in.Talents)

	hash, err := characters.ComputeRecipeHash(in.Name, appearanceJSON, statsJSON, talentsJSON)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("hash: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO character_recipes
			(owner_kind, owner_id, name, appearance_json, stats_json, talents_json, recipe_hash, created_by)
		VALUES ('player', $1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6, $1)
		RETURNING id
	`, playerID, in.Name, appearanceJSON, statsJSON, talentsJSON, hash).Scan(&recipeID); err != nil {
		return 0, 0, 0, fmt.Errorf("insert recipe: %w", err)
	}

	bakeRecipe, err := characters.LoadBakeRecipe(ctx, tx, recipeID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load recipe: %w", err)
	}
	out, err := characters.RunBake(ctx, tx, characters.BakeDeps{
		Store:            d.Characters.Store,
		Assets:           d.Characters.Assets,
		SystemDesignerID: d.Characters.SystemDesignerID,
	}, bakeRecipe, recipeID)
	if err != nil {
		return 0, 0, 0, err
	}
	return recipeID, out.BakeID, out.AssetID, nil
}

// updatePlayerCharacter handles POST /play/characters/{id}. Re-saves
// the recipe + re-bakes synchronously inside ONE transaction with
// the link update. Cross-player rewrites map to 404.
func updatePlayerCharacter(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Characters == nil {
			http.Error(w, "characters unavailable", http.StatusServiceUnavailable)
			return
		}
		p := PlayerFromContext(r.Context())
		if p == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		charID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		// Verify ownership BEFORE any mutation. FindPlayerCharacter
		// returns ErrForbidden on cross-player access.
		shell, err := d.Characters.FindPlayerCharacter(r.Context(), p.ID, charID)
		if err != nil {
			if errors.Is(err, characters.ErrPlayerCharNotFound) || errors.Is(err, characters.ErrForbidden) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("find player character", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		in, err := readPlayerCharacterBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			in.Name = shell.Name
		}

		recipeID, bakeID, bakeAssetID, err := updatePlayerCharacterAtomic(r.Context(), d, p.ID, charID, in)
		if err != nil {
			if errors.Is(err, characters.ErrPlayerCharNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("update player character", "err", err)
			http.Error(w, "save failed: "+err.Error(), http.StatusBadRequest)
			return
		}

		out := in
		out.ID = charID
		out.RecipeID = recipeID
		out.BakeID = bakeID
		out.BakeAsset = bakeAssetID
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// updatePlayerCharacterAtomic runs recipe insert + bake + link inside
// one transaction. Re-checks the player_id on the link UPDATE so a
// cross-player race can't squeeze in between the FindPlayerCharacter
// ownership check and the mutation.
func updatePlayerCharacterAtomic(
	ctx context.Context,
	d Deps,
	playerID, charID int64,
	in playerCharacterPayload,
) (recipeID, bakeID, bakeAssetID int64, err error) {
	if d.Characters.Store == nil || d.Characters.Assets == nil {
		return 0, 0, 0, errors.New("bake deps not configured")
	}
	tx, err := d.Characters.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rid, bid, baid, err := insertRecipeAndBakeInTx(ctx, tx, d, playerID, in)
	if err != nil {
		return 0, 0, 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE player_characters
		SET recipe_id = $1, active_bake_id = $2, name = $3, updated_at = now()
		WHERE id = $4 AND player_id = $5
	`, rid, bid, in.Name, charID, playerID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("link character: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, 0, 0, characters.ErrPlayerCharNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, 0, fmt.Errorf("commit: %w", err)
	}
	return rid, bid, baid, nil
}
