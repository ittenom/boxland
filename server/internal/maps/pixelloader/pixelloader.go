// Package pixelloader adapts the asset + entity services to the
// maps.TilePixelLoader interface so the maps package can drive the
// pixel-WFC engine without importing the asset pipeline directly.
//
// One Loader is wired at boot from cmd/boxland; both designer
// (preview/materialize) and runtime (transient regen) paths share it,
// so the LRU is warm across realms.
package pixelloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	"boxland/server/internal/maps/wfc"
)

// AssetReader is the slice of persistence.ObjectStore the loader needs.
// Defined locally so we don't pull the persistence package into our test
// surface.
type AssetReader interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// Loader implements maps.TilePixelLoader. Pulls an entity-type's sprite
// asset, decodes the PNG once per (asset, version), samples the frame's
// edges, caches the result.
type Loader struct {
	Pool   *pgxpool.Pool
	Assets *assets.Service
	Reader AssetReader
	Cache  *wfc.FingerprintCache

	// inFlight collapses concurrent fingerprint requests for the same
	// entity onto one decode + sample. Without this a project-wide
	// pixel-WFC preview kicks off N concurrent ObjectStore reads for
	// the same sheet on a cold cache.
	inFlightMu sync.Mutex
	inFlight   map[wfc.FingerprintKey]chan struct{}
}

// New constructs a Loader with sensible defaults.
func New(pool *pgxpool.Pool, assetSvc *assets.Service, reader AssetReader) *Loader {
	return &Loader{
		Pool:     pool,
		Assets:   assetSvc,
		Reader:   reader,
		Cache:    wfc.NewFingerprintCache(4096),
		inFlight: make(map[wfc.FingerprintKey]chan struct{}),
	}
}

// FingerprintFor implements maps.TilePixelLoader.
func (l *Loader) FingerprintFor(ctx context.Context, entityTypeID int64) ([4]wfc.EdgeFingerprint, error) {
	row, err := l.lookupEntity(ctx, entityTypeID)
	if err != nil {
		return [4]wfc.EdgeFingerprint{}, err
	}
	if row.SpriteAssetID == 0 {
		return [4]wfc.EdgeFingerprint{}, fmt.Errorf("entity %d has no sprite asset", entityTypeID)
	}
	key := wfc.FingerprintKey{
		EntityTypeID: entityTypeID,
		Frame:        row.AtlasIndex,
		AssetVersion: row.AssetUpdatedUnix,
	}
	if fp, ok := l.Cache.Get(key); ok {
		return fp, nil
	}

	// Single-flight: wait for any in-flight peer, then re-check the cache.
	l.inFlightMu.Lock()
	if ch, ok := l.inFlight[key]; ok {
		l.inFlightMu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return [4]wfc.EdgeFingerprint{}, ctx.Err()
		}
		if fp, ok := l.Cache.Get(key); ok {
			return fp, nil
		}
		// Peer didn't populate (its fetch failed); fall through to
		// retry locally — uncommon, and we charge ourselves the work.
	} else {
		done := make(chan struct{})
		l.inFlight[key] = done
		l.inFlightMu.Unlock()
		defer func() {
			l.inFlightMu.Lock()
			delete(l.inFlight, key)
			l.inFlightMu.Unlock()
			close(done)
		}()
	}

	// Fetch + decode the source PNG.
	asset, err := l.Assets.FindByID(ctx, row.SpriteAssetID)
	if err != nil {
		return [4]wfc.EdgeFingerprint{}, fmt.Errorf("asset lookup: %w", err)
	}
	rc, err := l.Reader.Get(ctx, asset.ContentAddressedPath)
	if err != nil {
		return [4]wfc.EdgeFingerprint{}, fmt.Errorf("asset read: %w", err)
	}
	raw, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return [4]wfc.EdgeFingerprint{}, fmt.Errorf("asset body: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		// Try PNG explicitly (image.Decode requires a registered format).
		img, err = png.Decode(bytes.NewReader(raw))
		if err != nil {
			return [4]wfc.EdgeFingerprint{}, fmt.Errorf("image decode: %w", err)
		}
	}

	// Frame rect: every Boxland tile is 32x32; row-major atlas index.
	cols := 1
	if md, derr := assets.DecodeTileSheetMetadata(asset.MetadataJSON); derr == nil && md.Cols > 0 {
		cols = md.Cols
	}
	tileSize := assets.TileSize
	idx := int(row.AtlasIndex)
	col := idx % cols
	rowN := idx / cols
	rect := image.Rect(
		col*tileSize, rowN*tileSize,
		(col+1)*tileSize, (rowN+1)*tileSize,
	)
	fp, err := wfc.ComputeFingerprint(img, rect)
	if err != nil {
		return [4]wfc.EdgeFingerprint{}, fmt.Errorf("fingerprint: %w", err)
	}
	l.Cache.Put(key, fp)
	return fp, nil
}

// entityRow is the slice of entity_types we need to fingerprint a tile.
// Loaded with a single SELECT so this method stays n+1-safe.
type entityRow struct {
	SpriteAssetID    int64
	AtlasIndex       int32
	AssetUpdatedUnix int64
}

// ErrEntityNotFound is returned when no entity_types row matches.
var ErrEntityNotFound = errors.New("pixelloader: entity not found")

func (l *Loader) lookupEntity(ctx context.Context, entityTypeID int64) (entityRow, error) {
	// Joined select so we cache against the asset's updated_at — when the
	// designer re-uploads a sheet the FingerprintKey changes and the
	// cached value is naturally bypassed (old entry will eventually be
	// evicted by LRU pressure, or pruned via Cache.Drop on demand).
	var r entityRow
	var spriteAssetID *int64
	var assetUpdated *int64
	err := l.Pool.QueryRow(ctx, `
		SELECT et.sprite_asset_id, et.atlas_index, EXTRACT(EPOCH FROM a.updated_at)::bigint
		FROM entity_types et
		LEFT JOIN assets a ON a.id = et.sprite_asset_id
		WHERE et.id = $1
	`, entityTypeID).Scan(&spriteAssetID, &r.AtlasIndex, &assetUpdated)
	if err != nil {
		return entityRow{}, fmt.Errorf("entity lookup: %w", err)
	}
	if spriteAssetID == nil {
		return r, nil // SpriteAssetID stays 0; caller handles
	}
	r.SpriteAssetID = *spriteAssetID
	if assetUpdated != nil {
		r.AssetUpdatedUnix = *assetUpdated
	}
	return r, nil
}
