// Boxland — palette-variant bake job.
//
// Per PLAN.md §1 "Palette swap": variants are pre-baked PNGs at publish
// time (NOT runtime shaders). One row in palette_variants is the *recipe*;
// one row in asset_variants is the *baked output*. Both use content-
// addressed paths so re-running the bake on identical inputs is free.
//
// The bake job runs INLINE in the publish transaction (no external queue,
// per PLAN.md §1 optimization #10). N variants per asset run via
// sync.WaitGroup so a publish with many variants completes in
// roughly max(per-variant-time) instead of sum.

package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/persistence"
)

// BakeJob owns a pool + object store + asset service. Constructed once at
// boot and shared across publish requests.
type BakeJob struct {
	Pool   *pgxpool.Pool
	Store  *persistence.ObjectStore
	Assets *Service
}

// NewBakeJob constructs the job with the given dependencies.
func NewBakeJob(pool *pgxpool.Pool, store *persistence.ObjectStore, svc *Service) *BakeJob {
	return &BakeJob{Pool: pool, Store: store, Assets: svc}
}

// BakeResult summarizes one bake run for the caller.
type BakeResult struct {
	AssetVariantID         int64
	AssetID                int64
	PaletteVariantID       int64
	BakedContentPath       string
	Reused                 bool   // true if status was already 'baked' with this exact path
	DurationMS             int64
	OutputBytes            int
}

// BakeForAsset bakes every palette variant defined for the given asset. Runs
// each variant in parallel via sync.WaitGroup. Returns one result per variant
// or, on first error, the partial results plus the error.
//
// The job is idempotent: a previously-baked variant whose recipe and source
// haven't changed returns Reused=true with no work performed. Source bytes
// are read once and shared across variants.
func (j *BakeJob) BakeForAsset(ctx context.Context, assetID int64) ([]BakeResult, error) {
	asset, err := j.Assets.FindByID(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if asset.Kind != KindSprite && asset.Kind != KindSpriteAnimated {
		return nil, fmt.Errorf("bake: kind %q has no palette variants", asset.Kind)
	}

	// Source bytes are needed by every variant; fetch once.
	sourceURL := j.Store.PublicURL(asset.ContentAddressedPath)
	srcBytes, err := j.fetchSource(ctx, sourceURL, asset.ContentAddressedPath)
	if err != nil {
		return nil, fmt.Errorf("fetch source: %w", err)
	}

	recipes, err := j.loadRecipes(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if len(recipes) == 0 {
		return nil, nil
	}

	results := make([]BakeResult, len(recipes))
	errs := make([]error, len(recipes))
	var wg sync.WaitGroup
	for i, recipe := range recipes {
		i, recipe := i, recipe
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := j.bakeOne(ctx, asset, recipe, srcBytes)
			results[i] = res
			errs[i] = err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// ---- internals ----

type recipeRow struct {
	ID               int64
	SourceToDestJSON []byte
}

// loadRecipes reads every palette_variant for the asset.
func (j *BakeJob) loadRecipes(ctx context.Context, assetID int64) ([]recipeRow, error) {
	rows, err := j.Pool.Query(ctx, `
		SELECT id, source_to_dest_json
		FROM palette_variants
		WHERE asset_id = $1
		ORDER BY id
	`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipeRow
	for rows.Next() {
		var r recipeRow
		if err := rows.Scan(&r.ID, &r.SourceToDestJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// fetchSource reads the source PNG from object storage. We use Get (which
// internally calls into the s3 client); this can be replaced with a
// streaming pre-signed URL fetch later if RAM pressure becomes a concern.
func (j *BakeJob) fetchSource(ctx context.Context, _ string, key string) ([]byte, error) {
	// Lean on the http path of the public URL for simplicity; the bake job
	// runs in the same process as the asset uploader, so we already have
	// the bytes if they were just uploaded -- but on a multi-process
	// deployment (or a re-bake at publish time) we fetch.
	// For v1, the simplest correct path is s3 GetObject via the store.
	r, err := j.Store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// bakeOne applies one recipe to source bytes and uploads the result. The
// output key is content-addressed (sha256 of (source-key || recipe-key)),
// so identical inputs always produce the same key — making this idempotent
// across re-runs.
func (j *BakeJob) bakeOne(ctx context.Context, asset *Asset, recipe recipeRow, src []byte) (BakeResult, error) {
	start := time.Now()

	// Decode the recipe.
	swap, err := decodeRecipe(recipe.SourceToDestJSON)
	if err != nil {
		_ = j.markFailed(ctx, asset.ID, recipe.ID, err.Error())
		return BakeResult{}, fmt.Errorf("recipe %d: %w", recipe.ID, err)
	}

	// Compute the deterministic output key BEFORE doing the heavy work, so
	// the "already-baked at this exact key" idempotency check is one
	// query rather than a re-render.
	outKey := bakeOutputKey(asset.ContentAddressedPath, swap)

	if existing, ok, err := j.findExistingBake(ctx, asset.ID, recipe.ID); err != nil {
		return BakeResult{}, err
	} else if ok && existing.path == outKey && existing.status == "baked" {
		return BakeResult{
			AssetVariantID:   existing.id,
			AssetID:          asset.ID,
			PaletteVariantID: recipe.ID,
			BakedContentPath: outKey,
			Reused:           true,
			DurationMS:       0,
		}, nil
	}

	// Decode source PNG.
	srcImg, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		_ = j.markFailed(ctx, asset.ID, recipe.ID, "decode source: "+err.Error())
		return BakeResult{}, fmt.Errorf("recipe %d: decode source: %w", recipe.ID, err)
	}

	// Apply the swap.
	out := remap(srcImg, swap)

	var outBuf bytes.Buffer
	if err := png.Encode(&outBuf, out); err != nil {
		_ = j.markFailed(ctx, asset.ID, recipe.ID, "encode: "+err.Error())
		return BakeResult{}, fmt.Errorf("recipe %d: encode: %w", recipe.ID, err)
	}

	// Upload to object storage. Wrap as a *bytes.Reader because the AWS SDK
	// v2 requires a seekable Reader for PutObject when running against a
	// plain-HTTP endpoint (e.g. MinIO in dev) without TLS + trailing
	// checksum. *bytes.Buffer is not seekable; *bytes.Reader is.
	outBytes := outBuf.Bytes()
	if err := j.Store.Put(ctx, outKey, "image/png", bytes.NewReader(outBytes), int64(len(outBytes))); err != nil {
		_ = j.markFailed(ctx, asset.ID, recipe.ID, "upload: "+err.Error())
		return BakeResult{}, fmt.Errorf("recipe %d: upload: %w", recipe.ID, err)
	}

	// Upsert the row to status='baked'.
	id, err := j.upsertBaked(ctx, asset.ID, recipe.ID, outKey)
	if err != nil {
		return BakeResult{}, fmt.Errorf("recipe %d: upsert: %w", recipe.ID, err)
	}

	return BakeResult{
		AssetVariantID:   id,
		AssetID:          asset.ID,
		PaletteVariantID: recipe.ID,
		BakedContentPath: outKey,
		Reused:           false,
		DurationMS:       time.Since(start).Milliseconds(),
		OutputBytes:      outBuf.Len(),
	}, nil
}

// existingBake captures a row from asset_variants at lookup time.
type existingBake struct {
	id     int64
	path   string
	status string
}

func (j *BakeJob) findExistingBake(ctx context.Context, assetID, recipeID int64) (existingBake, bool, error) {
	var e existingBake
	var path *string
	err := j.Pool.QueryRow(ctx, `
		SELECT id, content_addressed_path, status
		FROM asset_variants WHERE asset_id = $1 AND palette_variant_id = $2
	`, assetID, recipeID).Scan(&e.id, &path, &e.status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return existingBake{}, false, nil
		}
		return existingBake{}, false, err
	}
	if path != nil {
		e.path = *path
	}
	return e, true, nil
}

func (j *BakeJob) upsertBaked(ctx context.Context, assetID, recipeID int64, key string) (int64, error) {
	var id int64
	err := j.Pool.QueryRow(ctx, `
		INSERT INTO asset_variants (asset_id, palette_variant_id, content_addressed_path, status, baked_at)
		VALUES ($1, $2, $3, 'baked', now())
		ON CONFLICT (asset_id, palette_variant_id) DO UPDATE
		SET content_addressed_path = EXCLUDED.content_addressed_path,
		    status = 'baked',
		    baked_at = now(),
		    failure_reason = NULL
		RETURNING id
	`, assetID, recipeID, key).Scan(&id)
	return id, err
}

func (j *BakeJob) markFailed(ctx context.Context, assetID, recipeID int64, reason string) error {
	_, err := j.Pool.Exec(ctx, `
		INSERT INTO asset_variants (asset_id, palette_variant_id, status, failure_reason)
		VALUES ($1, $2, 'failed', $3)
		ON CONFLICT (asset_id, palette_variant_id) DO UPDATE
		SET status = 'failed', failure_reason = EXCLUDED.failure_reason
	`, assetID, recipeID, reason)
	return err
}

// ---- recipe ----

// recipeSwap is the in-memory form of the source_to_dest_json column. Keys
// are 0xRRGGBBAA values; values are the same. Storing as map[uint32]uint32
// makes the per-pixel hot-loop a hash lookup with no string allocations.
type recipeSwap map[uint32]uint32

// decodeRecipe parses the source_to_dest_json column, which is a JSON
// object whose keys and values are decimal-string representations of
// 0xRRGGBBAA values. Strings are necessary because JSON keys must be strings.
func decodeRecipe(raw []byte) (recipeSwap, error) {
	var m map[string]json.Number
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := make(recipeSwap, len(m))
	for k, v := range m {
		src, err := parseColor(k)
		if err != nil {
			return nil, fmt.Errorf("source color %q: %w", k, err)
		}
		dst, err := parseColor(v.String())
		if err != nil {
			return nil, fmt.Errorf("dest color for %q: %w", k, err)
		}
		out[src] = dst
	}
	return out, nil
}

// parseColor accepts a decimal string (0..4_294_967_295). 0xRRGGBBAA shape
// is conventional but not enforced -- recipes can include alpha-only or
// any 32-bit value.
func parseColor(s string) (uint32, error) {
	var n uint64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, err
	}
	return uint32(n), nil
}

// ---- pixel pass ----

// remap returns a new NRGBA image with the swap applied. Each source pixel
// not present in the swap map is copied unchanged. Alpha is included in the
// match key — recipes can swap colors at specific opacities.
func remap(src image.Image, swap recipeSwap) *image.NRGBA {
	bounds := src.Bounds()
	out := image.NewNRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			key := uint32(c.R)<<24 | uint32(c.G)<<16 | uint32(c.B)<<8 | uint32(c.A)
			if dst, ok := swap[key]; ok {
				out.SetNRGBA(x, y, color.NRGBA{
					R: uint8(dst >> 24),
					G: uint8(dst >> 16),
					B: uint8(dst >> 8),
					A: uint8(dst),
				})
			} else {
				out.SetNRGBA(x, y, c)
			}
		}
	}
	return out
}

// bakeOutputKey produces the content-addressed key for a baked variant.
// Combines the source key + a deterministic hash of the recipe so the same
// (source, recipe) always lands at the same path.
func bakeOutputKey(sourceKey string, swap recipeSwap) string {
	h := sha256.New()
	h.Write([]byte(sourceKey))
	// Sort keys so the hash is deterministic regardless of map iteration order.
	keys := make([]uint32, 0, len(swap))
	for k := range swap {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		var buf [8]byte
		buf[0] = byte(k >> 24)
		buf[1] = byte(k >> 16)
		buf[2] = byte(k >> 8)
		buf[3] = byte(k)
		v := swap[k]
		buf[4] = byte(v >> 24)
		buf[5] = byte(v >> 16)
		buf[6] = byte(v >> 8)
		buf[7] = byte(v)
		h.Write(buf[:])
	}
	sum := h.Sum(nil)
	hexSum := hex.EncodeToString(sum)
	return "asset_variants/" + hexSum[:2] + "/" + hexSum[2:4] + "/" + hexSum
}
