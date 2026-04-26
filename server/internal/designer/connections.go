package designer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"boxland/server/views"
)

// ConnectionsForAsset reports what entity types reference this asset.
// Counts both `entity_types.sprite_asset_id` and the `audio_emitter`
// component's `sound_id` field. Used by the asset detail's rail and by
// the asset-grid orphan badge.
func ConnectionsForAsset(ctx context.Context, d Deps, assetID int64) (*views.RailData, error) {
	if d.Assets == nil {
		return nil, nil
	}
	rail := &views.RailData{}

	// Find entity types whose sprite is this asset.
	rows, err := d.Assets.Pool.Query(ctx, `
		SELECT id, name FROM entity_types WHERE sprite_asset_id = $1
		ORDER BY name LIMIT 12
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("query entity refs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		rail.UsedBy = append(rail.UsedBy, views.RailRef{
			Kind:  "entity",
			Label: name,
			Meta:  "sprite",
			Href:  fmt.Sprintf("/design/entities/%d", id),
		})
	}

	// Audio component refs (only for audio assets — sprites won't match).
	audioRows, err := d.Assets.Pool.Query(ctx, `
		SELECT et.id, et.name
		FROM entity_components ec
		JOIN entity_types et ON et.id = ec.entity_type_id
		WHERE ec.kind = 'audio_emitter'
		  AND (ec.config_json->>'sound_id')::bigint = $1
		ORDER BY et.name LIMIT 12
	`, assetID)
	if err == nil {
		defer audioRows.Close()
		for audioRows.Next() {
			var id int64
			var name string
			if err := audioRows.Scan(&id, &name); err != nil {
				return nil, err
			}
			rail.UsedBy = append(rail.UsedBy, views.RailRef{
				Kind:  "entity",
				Label: name,
				Meta:  "audio_emitter",
				Href:  fmt.Sprintf("/design/entities/%d", id),
			})
		}
	}

	if len(rail.UsedBy) == 0 {
		rail.Suggestions = []views.RailCTA{
			{Label: "+ Make this an entity", Href: "/design/entities"},
		}
	}
	return rail, nil
}

// AssetUsageMap returns a map of asset id → number of entities
// referencing it (sprite + audio component refs combined). Cheap enough
// to call on every Asset Manager render so orphan badges are accurate.
//
// The map is dense: every asset id present in the input slice gets a
// key, even if the count is zero (so the templ can render "orphan"
// tags reliably without nil-checks).
func AssetUsageMap(ctx context.Context, d Deps, assetIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(assetIDs))
	for _, id := range assetIDs {
		out[id] = 0
	}
	if d.Assets == nil || len(assetIDs) == 0 {
		return out, nil
	}

	// Sprite refs
	rows, err := d.Assets.Pool.Query(ctx, `
		SELECT sprite_asset_id, count(*)
		FROM entity_types
		WHERE sprite_asset_id = ANY($1)
		GROUP BY sprite_asset_id
	`, assetIDs)
	if err != nil {
		return out, fmt.Errorf("sprite refs: %w", err)
	}
	if err := scanCountInto(rows, out); err != nil {
		return out, err
	}

	// Audio component refs (only if an audio_emitter exists)
	audioRows, err := d.Assets.Pool.Query(ctx, `
		SELECT (config_json->>'sound_id')::bigint AS asset_id, count(*)
		FROM entity_components
		WHERE kind = 'audio_emitter'
		  AND config_json ? 'sound_id'
		  AND (config_json->>'sound_id')::bigint = ANY($1)
		GROUP BY asset_id
	`, assetIDs)
	if err == nil {
		if err := scanCountInto(audioRows, out); err != nil {
			slog.Warn("audio refs scan", "err", err)
		}
	}
	return out, nil
}

// scanCountInto reads (int64, int) rows and adds the count to the
// target map at the given id. Helper because both the sprite and audio
// queries return the same shape.
func scanCountInto(rows pgx.Rows, target map[int64]int) error {
	defer rows.Close()
	for rows.Next() {
		var id int64
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return err
		}
		target[id] += count
	}
	return nil
}

// ConnectionsForEntity reports both directions for an entity type:
// what assets / entities it depends on, what maps + entities reference
// it, and a suggested-next CTA list.
func ConnectionsForEntity(ctx context.Context, d Deps, entityID int64) (*views.RailData, error) {
	rail := &views.RailData{}

	if d.Entities == nil {
		return rail, nil
	}
	et, err := d.Entities.FindByID(ctx, entityID)
	if err != nil {
		return rail, err
	}

	// USES: sprite asset
	if et.SpriteAssetID != nil && d.Assets != nil {
		if a, err := d.Assets.FindByID(ctx, *et.SpriteAssetID); err == nil {
			rail.Uses = append(rail.Uses, views.RailRef{
				Kind:  "asset",
				Label: a.Name,
				Meta:  "sprite",
				Href:  fmt.Sprintf("/design/assets/%d", a.ID),
			})
		}
	}

	// USED ON MAPS: count placements per map
	if d.Maps != nil {
		mapRows, err := d.Entities.Pool.Query(ctx, `
			SELECT m.id, m.name, count(mt.entity_type_id)
			FROM map_tiles mt
			JOIN maps m ON m.id = mt.map_id
			WHERE mt.entity_type_id = $1
			GROUP BY m.id, m.name
			ORDER BY m.name LIMIT 12
		`, entityID)
		if err == nil {
			defer mapRows.Close()
			for mapRows.Next() {
				var id int64
				var name string
				var count int
				if err := mapRows.Scan(&id, &name, &count); err != nil {
					return rail, err
				}
				rail.UsedBy = append(rail.UsedBy, views.RailRef{
					Kind:  "map",
					Label: name,
					Meta:  fmt.Sprintf("× %d", count),
					Href:  fmt.Sprintf("/design/maps/%d", id),
				})
			}
		}
	}

	// SUGGESTED NEXT: contextual CTAs depending on what's missing.
	if et.SpriteAssetID == nil {
		rail.Suggestions = append(rail.Suggestions, views.RailCTA{
			Label: "+ Pick a sprite asset", Href: "/design/assets",
		})
	}
	rail.Suggestions = append(rail.Suggestions, views.RailCTA{
		Label: "▶ Test on a map", Href: "/design/maps",
	})
	if len(rail.UsedBy) == 0 {
		rail.Suggestions = append(rail.Suggestions, views.RailCTA{
			Label: "+ Place on a map", Href: "/design/maps",
		})
	}
	return rail, nil
}
