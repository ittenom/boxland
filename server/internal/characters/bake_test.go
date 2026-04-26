// Boxland — characters: bake-pipeline integration tests.
//
// Drives RunBake against a real Postgres (testdb.New) and a real MinIO
// (makeBakeStore). Each test seeds: a designer, two sprite assets
// (uploaded as PNG bytes to MinIO), two slots, two parts referencing
// those assets, and a recipe selecting both parts. The test then runs
// RunBake inside a tx and asserts:
//
//   * the composed PNG ends up at the expected content-addressed key,
//   * a sprite assets row was created with sheet metadata + animations,
//   * a character_bakes row landed in 'baked' status,
//   * a second RunBake on the same recipe is a no-op (Reused=true).
//
// MinIO is mandatory: tests skip cleanly when it's unreachable.

package characters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/assets"
	"boxland/server/internal/characters"
	"boxland/server/internal/persistence"
)

// makeBakeStore connects to the dev MinIO. Skips when unreachable.
// Mirrors assets/upload_test.go's helper, kept private to this package
// so the test fixtures are self-contained.
func makeBakeStore(t *testing.T) *persistence.ObjectStore {
	t.Helper()
	cfg := persistence.ObjectStoreConfig{
		Endpoint:        "http://localhost:9000",
		Region:          "us-east-1",
		Bucket:          "boxland-assets",
		AccessKeyID:     "boxland",
		SecretAccessKey: "boxland_dev_secret",
		UsePathStyle:    true,
		PublicBaseURL:   "http://localhost:9000/boxland-assets",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := persistence.NewObjectStore(ctx, cfg)
	if err != nil {
		t.Skipf("minio unavailable: %v", err)
	}
	return store
}

// solidPNG builds an NxN PNG flooded with one color. Returns the bytes.
func solidPNG(t *testing.T, n int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}

// uploadPart uploads a tiny single-frame PNG to MinIO and creates a
// matching assets row + a character_parts row. Returns the part.
func uploadPart(
	t *testing.T,
	ctx context.Context,
	f *fixture,
	store *persistence.ObjectStore,
	slotID int64,
	name string,
	c color.NRGBA,
	frameMap string,
) *characters.Part {
	t.Helper()
	body := solidPNG(t, 32, c)
	key := persistence.ContentAddressedKey("assets", body)
	if err := store.Put(ctx, key, "image/png", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("put: %v", err)
	}
	asvc := assets.New(f.pool)
	asset, err := asvc.Create(ctx, assets.CreateInput{
		Kind:                 assets.KindSprite,
		Name:                 "src-" + name,
		ContentAddressedPath: key,
		OriginalFormat:       "png",
		MetadataJSON:         []byte(`{"grid_w":32,"grid_h":32,"cols":1,"rows":1,"frame_count":1}`),
		CreatedBy:            f.designerID,
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	part, err := f.svc.CreatePart(ctx, characters.CreatePartInput{
		SlotID:       slotID,
		AssetID:      asset.ID,
		Name:         name,
		FrameMapJSON: []byte(frameMap),
		CreatedBy:    f.designerID,
	})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	return part
}

// seedRecipe inserts a character_recipes row with the given parts as
// the appearance selection. Returns the recipe id and a BakeRecipe
// ready for RunBake.
func seedRecipe(
	t *testing.T,
	ctx context.Context,
	f *fixture,
	parts []*characters.Part,
) (int64, characters.BakeRecipe) {
	t.Helper()

	slotByID := func(id int64) characters.Slot {
		got, err := f.svc.FindSlotByID(ctx, id)
		if err != nil {
			t.Fatalf("find slot %d: %v", id, err)
		}
		return *got
	}
	slots := make([]characters.AppearanceSlot, len(parts))
	selections := make([]characters.BakedSelection, len(parts))
	for i, p := range parts {
		s := slotByID(p.SlotID)
		slots[i] = characters.AppearanceSlot{SlotKey: s.Key, PartID: p.ID}
		layer := s.DefaultLayerOrder
		if p.LayerOrder != nil {
			layer = *p.LayerOrder
		}
		selections[i] = characters.BakedSelection{Slot: s, Part: *p, LayerOrder: layer}
	}
	appearance, _ := json.Marshal(characters.AppearanceSelection{Slots: slots})
	hash, _ := characters.ComputeRecipeHash("Test recipe", appearance, nil, nil)

	var recipeID int64
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO character_recipes
			(owner_kind, owner_id, name, appearance_json, recipe_hash, created_by)
		VALUES ('designer', $1, 'Test recipe', $2::jsonb, $3, $1)
		RETURNING id
	`, f.designerID, appearance, hash).Scan(&recipeID); err != nil {
		t.Fatalf("seed recipe: %v", err)
	}

	return recipeID, characters.BakeRecipe{
		Name:           "Test recipe",
		OwnerKind:      characters.OwnerKindDesigner,
		OwnerID:        f.designerID,
		Selections:     selections,
		AppearanceJSON: appearance,
	}
}

// withBakeTx runs fn inside a publish-style tx that's always rolled back
// — but for the bake test the Store side effect is committed (object
// storage isn't transactional). That's fine: identical bytes at the
// content-addressed key are a no-op, and the per-test DB drop cleans
// up the assets row that referenced the key.
func withBakeTx(t *testing.T, f *fixture, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := f.pool.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	fn(tx)
}

// withBakeCommit runs fn inside a tx that's committed on success. Used
// when the test wants to read the bake row from a follow-up tx (e.g.
// to test the "second bake reuses" path).
func withBakeCommit(t *testing.T, f *fixture, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := f.pool.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	fn(tx)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cases
// ---------------------------------------------------------------------------

func TestBake_HappyPath_TwoLayers(t *testing.T) {
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()

	// Use the seeded `body` and `hair_front` slots.
	slots, _ := f.svc.ListSlots(ctx)
	bodySlot := slots[0] // body
	var hairFront characters.Slot
	for _, s := range slots {
		if s.Key == "hair_front" {
			hairFront = s
			break
		}
	}

	red := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	blue := color.NRGBA{R: 0, G: 0, B: 255, A: 255}
	body := uploadPart(t, ctx, f, store, bodySlot.ID, "body", red, `{"idle":[0,0]}`)
	hair := uploadPart(t, ctx, f, store, hairFront.ID, "hair", blue, `{"idle":[0,0]}`)

	recipeID, br := seedRecipe(t, ctx, f, []*characters.Part{body, hair})

	withBakeCommit(t, f, func(tx pgx.Tx) {
		out, err := characters.RunBake(ctx, tx, characters.BakeDeps{Store: store, Assets: assets.New(f.pool)}, br, recipeID)
		if err != nil {
			t.Fatalf("RunBake: %v", err)
		}
		if out.Reused {
			t.Errorf("first bake should not be Reused")
		}
		if out.AssetID == 0 {
			t.Errorf("AssetID was 0")
		}
		if out.OutputKey == "" {
			t.Errorf("OutputKey was empty")
		}
		if len(out.Anims) != 1 || out.Anims[0].Name != "idle" {
			t.Errorf("anims: %+v", out.Anims)
		}
	})

	// Verify the bake row + asset row landed in the public DB view.
	ctx2 := context.Background()
	var status string
	if err := f.pool.QueryRow(ctx2, `SELECT status FROM character_bakes WHERE recipe_id = $1`, recipeID).Scan(&status); err != nil {
		t.Fatalf("read bake row: %v", err)
	}
	if status != "baked" {
		t.Errorf("status = %q, want baked", status)
	}

	// Sprite asset row must have kind='sprite' and the right metadata.
	var assetCount int
	if err := f.pool.QueryRow(ctx2, `
		SELECT count(*) FROM assets WHERE kind = 'sprite' AND $1 = ANY(tags)
	`, "character_bake").Scan(&assetCount); err != nil {
		t.Fatalf("count baked assets: %v", err)
	}
	if assetCount < 1 {
		t.Errorf("expected at least one character_bake-tagged sprite asset, got %d", assetCount)
	}
}

func TestBake_DedupsByRecipeHash(t *testing.T) {
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()

	slots, _ := f.svc.ListSlots(ctx)
	body := uploadPart(t, ctx, f, store, slots[0].ID, "body", color.NRGBA{255, 0, 0, 255}, `{"idle":[0,0]}`)
	recipeID, br := seedRecipe(t, ctx, f, []*characters.Part{body})

	deps := characters.BakeDeps{Store: store, Assets: assets.New(f.pool)}

	var firstKey string
	withBakeCommit(t, f, func(tx pgx.Tx) {
		out, err := characters.RunBake(ctx, tx, deps, br, recipeID)
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		if out.Reused {
			t.Errorf("first should not Reused")
		}
		firstKey = out.OutputKey
	})

	// Second call with the same recipe inside a fresh tx — should be a
	// reuse, identical key, no new assets row.
	var assetsBefore int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE 'character_bake' = ANY(tags)`).Scan(&assetsBefore)

	withBakeCommit(t, f, func(tx pgx.Tx) {
		out, err := characters.RunBake(ctx, tx, deps, br, recipeID)
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if !out.Reused {
			t.Errorf("second bake should be Reused")
		}
		if out.OutputKey != firstKey {
			t.Errorf("key drift: %q vs %q", firstKey, out.OutputKey)
		}
	})

	var assetsAfter int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE 'character_bake' = ANY(tags)`).Scan(&assetsAfter)
	if assetsAfter != assetsBefore {
		t.Errorf("dedup leaked: assets went from %d to %d", assetsBefore, assetsAfter)
	}
}

func TestBake_RejectsRecipeWithNoCommonAnimation(t *testing.T) {
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()

	slots, _ := f.svc.ListSlots(ctx)
	bodySlot := slots[0]
	var hairSlot characters.Slot
	for _, s := range slots {
		if s.Key == "hair_front" {
			hairSlot = s
		}
	}
	// The two parts have NO overlapping animation key.
	body := uploadPart(t, ctx, f, store, bodySlot.ID, "body", color.NRGBA{255, 0, 0, 255}, `{"idle":[0,0]}`)
	hair := uploadPart(t, ctx, f, store, hairSlot.ID, "hair", color.NRGBA{0, 0, 255, 255}, `{"walk":[0,0]}`)

	recipeID, br := seedRecipe(t, ctx, f, []*characters.Part{body, hair})

	withBakeTx(t, f, func(tx pgx.Tx) {
		_, err := characters.RunBake(ctx, tx, characters.BakeDeps{Store: store, Assets: assets.New(f.pool)}, br, recipeID)
		if err == nil {
			t.Fatal("expected validation error for empty animation intersection")
		}
	})
}

func TestBake_RejectsPlayerOwner_WithoutSystemDesigner(t *testing.T) {
	// Player bakes are allowed only when BakeDeps.SystemDesignerID is
	// set (Phase 4 player flow). Without it the bake errors with a
	// helpful message — this test pins the guard.
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()

	slots, _ := f.svc.ListSlots(ctx)
	body := uploadPart(t, ctx, f, store, slots[0].ID, "body", color.NRGBA{255, 0, 0, 255}, `{"idle":[0,0]}`)
	recipeID, br := seedRecipe(t, ctx, f, []*characters.Part{body})

	br.OwnerKind = characters.OwnerKindPlayer

	withBakeTx(t, f, func(tx pgx.Tx) {
		_, err := characters.RunBake(ctx, tx,
			characters.BakeDeps{Store: store, Assets: assets.New(f.pool)},
			br, recipeID)
		if err == nil {
			t.Fatal("expected error when player recipe baked without SystemDesignerID")
		}
	})
}

func TestBake_AcceptsPlayerOwner_WithSystemDesigner(t *testing.T) {
	// With a SystemDesignerID configured, player recipes bake just
	// like designer recipes; the asset's created_by attribution falls
	// to the system designer id.
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()

	slots, _ := f.svc.ListSlots(ctx)
	body := uploadPart(t, ctx, f, store, slots[0].ID, "body", color.NRGBA{255, 0, 0, 255}, `{"idle":[0,0]}`)
	recipeID, br := seedRecipe(t, ctx, f, []*characters.Part{body})

	br.OwnerKind = characters.OwnerKindPlayer
	// Designer fixture id reused as the system designer for test purposes.
	deps := characters.BakeDeps{Store: store, Assets: assets.New(f.pool), SystemDesignerID: f.designerID}

	withBakeCommit(t, f, func(tx pgx.Tx) {
		out, err := characters.RunBake(ctx, tx, deps, br, recipeID)
		if err != nil {
			t.Fatalf("RunBake: %v", err)
		}
		if out.AssetID == 0 {
			t.Errorf("expected an asset id")
		}
	})
}

func TestBake_OutputPNGComposesLayersInOrder(t *testing.T) {
	f := setup(t)
	store := makeBakeStore(t)
	ctx := context.Background()

	slots, _ := f.svc.ListSlots(ctx)
	bodySlot := slots[0]
	var hairSlot characters.Slot
	for _, s := range slots {
		if s.Key == "hair_front" {
			hairSlot = s
		}
	}
	// Body is opaque red; hair is opaque blue with full coverage.
	// hair_front has a higher default_layer_order than body, so the
	// composed pixel should be blue.
	body := uploadPart(t, ctx, f, store, bodySlot.ID, "body", color.NRGBA{255, 0, 0, 255}, `{"idle":[0,0]}`)
	hair := uploadPart(t, ctx, f, store, hairSlot.ID, "hair", color.NRGBA{0, 0, 255, 255}, `{"idle":[0,0]}`)
	recipeID, br := seedRecipe(t, ctx, f, []*characters.Part{body, hair})

	deps := characters.BakeDeps{Store: store, Assets: assets.New(f.pool)}
	var outputKey string
	withBakeCommit(t, f, func(tx pgx.Tx) {
		out, err := characters.RunBake(ctx, tx, deps, br, recipeID)
		if err != nil {
			t.Fatalf("RunBake: %v", err)
		}
		outputKey = out.OutputKey
	})

	// Read back the PNG.
	rc, err := store.Get(ctx, outputKey)
	if err != nil {
		t.Fatalf("get output: %v", err)
	}
	defer rc.Close()
	img, err := png.Decode(rc)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if img.Bounds().Dx() < characters.FrameSize || img.Bounds().Dy() < characters.FrameSize {
		t.Fatalf("output PNG too small: %v", img.Bounds())
	}
	got := color.NRGBAModel.Convert(img.At(16, 16)).(color.NRGBA)
	want := color.NRGBA{0, 0, 255, 255} // blue (hair) on top of red (body)
	if got != want {
		t.Errorf("center pixel = %+v, want %+v", got, want)
	}
}
