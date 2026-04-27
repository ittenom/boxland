package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"boxland/server/internal/persistence"
)

// jsonMarshal is a thin wrapper for marshalling metadata into the JSONB
// column. Kept local so callers don't need to import encoding/json.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// MaxUploadBytes caps a single uploaded file. PNG sprite sheets and small
// audio clips both fit comfortably; raise here if the bake job ever
// produces variants larger than the source.
const MaxUploadBytes = 16 * 1024 * 1024 // 16 MiB

// UploadResult is the outcome of one HTTP upload.
type UploadResult struct {
	Asset      *Asset
	Reused     bool   // true if an asset with the same content_addressed_path already existed
	OriginalFn string // original filename from the multipart form
	// TileCells / TileMeta are populated for kind=tile uploads.
	// See MultiUploadResult for the long form.
	TileCells    []TileCell
	TileMeta     TileSheetMetadata
	SpriteImport *ImportResult
}

// SupportedContentTypes maps the sniffed MIME type to the kind it produces.
// We use sniffing (not the client-supplied type) because the client may lie
// or guess wrong. Anything outside this set is rejected with 415.
var SupportedContentTypes = map[string]struct {
	Kind   Kind
	Format string
}{
	"image/png":   {KindSprite, "png"},
	"audio/wav":   {KindAudio, "wav"},
	"audio/x-wav": {KindAudio, "wav"},
	"audio/wave":  {KindAudio, "wav"},
	"audio/ogg":   {KindAudio, "ogg"},
	"audio/mpeg":  {KindAudio, "mp3"},
}

const (
	KindOverrideSpriteSheet    = "sprite_sheet"
	KindOverrideAnimatedSprite = "animated_sprite"
)

// NormalizeUploadKind maps UI-only upload modes onto persisted asset kinds.
// Sprite sheets and animated sprites are still sprite assets; the importer
// persists frame metadata + animation rows alongside the asset.
func NormalizeUploadKind(raw string) Kind {
	switch strings.TrimSpace(raw) {
	case "", "auto":
		return ""
	case KindOverrideSpriteSheet, KindOverrideAnimatedSprite:
		return KindSprite
	default:
		return Kind(raw)
	}
}

// Errors returned by Upload. Stable for HTTP handler mapping.
var (
	ErrNoFile                 = errors.New("upload: no file in request")
	ErrTooLarge               = errors.New("upload: file exceeds size limit")
	ErrUnsupportedContentType = errors.New("upload: unsupported content type")
)

// Upload reads the first file from a multipart request, validates it, pushes
// it to object storage at a content-addressed path, and inserts an asset row.
//
// kindOverride lets the handler force a kind (e.g. an image arriving on the
// "tile sheets" tab is a tile, not a sprite). Pass "" to use the default
// from SupportedContentTypes.
//
// The caller (HTTP handler in internal/designer) supplies the designer id
// from the session; this method makes no auth decisions of its own.
//
// Single-file path retained for the pixel-editor export (postAssetReplace)
// which always sends one PNG. Multi-file uploads from the asset modal go
// through UploadMany.
func (s *Service) Upload(
	ctx context.Context,
	r *http.Request,
	store *persistence.ObjectStore,
	designerID int64,
	kindOverride Kind,
) (*UploadResult, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxUploadBytes+1)
	if err := r.ParseMultipartForm(MaxUploadBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, maxBytesErr.Limit)
		}
		return nil, fmt.Errorf("parse multipart: %w", err)
	}
	_, header, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, ErrNoFile
		}
		return nil, fmt.Errorf("read file: %w", err)
	}
	return s.uploadFromHeader(ctx, header, nil, store, designerID, kindOverride)
}

// UploadMany reads every file under the multipart "files" field and runs
// each through the same content-addressed pipeline. Returns one
// UploadResult per file in the order the browser sent them. A failed
// file produces an entry with a non-nil Err and does not stop the rest;
// callers (HTTP handler) summarize the totals to the user.
//
// The total request size is capped at MaxUploadBytes * MaxFilesPerUpload
// to keep memory predictable. Per-file size still honors MaxUploadBytes
// individually.
func (s *Service) UploadMany(
	ctx context.Context,
	r *http.Request,
	store *persistence.ObjectStore,
	designerID int64,
	kindOverride Kind,
) ([]MultiUploadResult, error) {
	totalCap := int64(MaxUploadBytes) * int64(MaxFilesPerUpload)
	r.Body = http.MaxBytesReader(nil, r.Body, totalCap+1)
	if err := r.ParseMultipartForm(totalCap); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, fmt.Errorf("%w: %d bytes total", ErrTooLarge, maxBytesErr.Limit)
		}
		return nil, fmt.Errorf("parse multipart: %w", err)
	}
	if r.MultipartForm == nil {
		return nil, ErrNoFile
	}
	headers := r.MultipartForm.File["files"]
	// Back-compat: a single file under "file" still works through this path.
	if len(headers) == 0 {
		headers = r.MultipartForm.File["file"]
	}
	if len(headers) == 0 {
		return nil, ErrNoFile
	}
	if len(headers) > MaxFilesPerUpload {
		return nil, fmt.Errorf("%w: %d files (max %d)", ErrTooLarge, len(headers), MaxFilesPerUpload)
	}

	out := make([]MultiUploadResult, 0, len(headers))
	for _, h := range headers {
		// Pass the full sibling list so a PNG can pick up its
		// matching .json sidecar without a parser-override.
		res, err := s.uploadFromHeader(ctx, h, headers, store, designerID, kindOverride)
		entry := MultiUploadResult{OriginalFn: h.Filename}
		if err != nil {
			entry.Err = err
			out = append(out, entry)
			continue
		}
		entry.Asset = res.Asset
		entry.Reused = res.Reused
		entry.TileCells = res.TileCells
		entry.TileMeta = res.TileMeta
		entry.SpriteImport = res.SpriteImport
		out = append(out, entry)
	}
	return out, nil
}

// uploadFromHeader is the shared per-file pipeline used by both Upload
// and UploadMany. Reads, dedups, stores, inserts.
//
// `siblings` is the full multipart-form file slice for multi-file
// uploads (so a PNG can pick up its sidecar JSON automatically). Pass
// nil for single-file uploads.
func (s *Service) uploadFromHeader(
	ctx context.Context,
	header *multipart.FileHeader,
	siblings []*multipart.FileHeader,
	store *persistence.ObjectStore,
	designerID int64,
	kindOverride Kind,
) (*UploadResult, error) {
	if header == nil {
		return nil, ErrNoFile
	}
	if header.Size > MaxUploadBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, header.Size)
	}
	file, err := header.Open()
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, MaxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > MaxUploadBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, MaxUploadBytes)
	}
	if len(body) == 0 {
		return nil, ErrNoFile
	}

	sniffed := http.DetectContentType(body)
	supported, ok := SupportedContentTypes[normalizeContentType(sniffed)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedContentType, sniffed)
	}

	kind := supported.Kind
	if kindOverride != "" {
		kind = NormalizeUploadKind(string(kindOverride))
	}
	if kindOverride == "" && supported.Kind == KindSprite && isAutoTileSheet(body) {
		kind = KindTile
	}
	originalName := header.Filename
	displayName := defaultDisplayName(originalName)

	// Tile-sheet pre-flight: slice into 32x32 cells before we touch
	// object storage so a malformed sheet (odd dimensions, fully
	// transparent) fails fast with a clean error instead of leaving
	// an unusable asset row behind. The cells + metadata feed into
	// the per-cell entity_type fan-out in the upload handler.
	var tileCells []TileCell
	var tileMD TileSheetMetadata
	if kind == KindTile {
		cells, md, err := SliceTileSheet(body)
		if err != nil {
			return nil, err
		}
		tileCells = cells
		tileMD = md
	}

	// Sprite-sheet pre-flight: auto-detect the importer (Aseprite if
	// a sidecar is alongside, otherwise uniform-grid auto-slice with
	// canonical 32×32 cells), and synthesize walk_*/idle animations
	// for the common 4-row top-down layout. Importer failures here
	// are fatal (same policy as tile pre-flight): a bad PNG should
	// never produce an asset row with no animations.
	//
	// Sidecar source: an upload header named "<base>.json" alongside
	// "<base>.png" — we check the multipart form for it. The form is
	// parsed once at the top of (Upload|UploadMany), so accessing
	// r.MultipartForm here is safe.
	var spriteImport *ImportResult
	if kind == KindSprite && s.Importers != nil {
		sidecar := readSidecar(header, siblings)
		ir, err := DefaultSpriteImport(ctx, s.Importers, header.Filename, body, sidecar, AutoSliceConfig{})
		if err != nil {
			return nil, err
		}
		spriteImport = ir
	}

	key := persistence.ContentAddressedKey("assets", body)

	if existing, err := s.FindByContentPath(ctx, kind, key); err == nil {
		// Re-uploads of an existing tile sheet still surface the slice
		// info so the handler can backfill any cells that lacked an
		// entity (e.g. designer ran the upload before tile auto-slice
		// shipped, or deleted some cells and wants them back).
		//
		// Symmetrically for sprites: if the existing row predates the
		// upload-time animation persistence (or had its rows wiped by
		// a manual cleanup), backfill them so the runtime catalog
		// always sees a populated set on a re-upload. Idempotent —
		// ReplaceAnimations with the same input is a no-op net of
		// the DELETE+INSERT pair.
		if spriteImport != nil && len(spriteImport.Animations) > 0 {
			rows, lerr := s.ListAnimations(ctx, existing.ID)
			if lerr == nil && len(rows) == 0 {
				if rerr := s.ReplaceAnimations(ctx, existing.ID, spriteImport.Animations); rerr != nil {
					slog.Warn("upload: backfill animations on reuse",
						"asset_id", existing.ID, "err", rerr)
				}
			}
		}
		return &UploadResult{
			Asset: existing, Reused: true, OriginalFn: originalName,
			TileCells: tileCells, TileMeta: tileMD, SpriteImport: spriteImport,
		}, nil
	} else if !errors.Is(err, ErrAssetNotFound) {
		return nil, fmt.Errorf("dedup lookup: %w", err)
	}

	if err := store.Put(ctx, key, sniffed, bytes.NewReader(body), int64(len(body))); err != nil {
		return nil, fmt.Errorf("upload to storage: %w", err)
	}

	metadata := []byte("{}")
	if kind == KindAudio {
		md, err := InspectAudio(body)
		if err == nil {
			if b, jerr := jsonMarshal(md); jerr == nil {
				metadata = b
			}
		}
	} else if kind == KindTile {
		if b, jerr := MarshalTileSheetMetadata(tileMD); jerr == nil {
			metadata = b
		}
	} else if kind == KindSprite && spriteImport != nil {
		// Persist the sheet metadata (grid, frame count, source) so
		// the runtime catalog can rebuild source rects without
		// re-parsing the PNG. See web/src/game/catalog.ts.
		if b, jerr := jsonMarshal(spriteImport.SheetMetadata); jerr == nil {
			metadata = b
		}
	}

	// Compute dominant color for image kinds so the "sort by color"
	// view in Asset Manager doesn't need a backfill pass for fresh
	// uploads. Failure is non-fatal; the lazy backfill in
	// Service.EnsureDominantColors picks up any holes later.
	var dominantColor *int64
	if kind == KindSprite || kind == KindTile || kind == KindUIPanel {
		if packed, ok := ComputeDominantColor(body); ok {
			v := int64(packed)
			dominantColor = &v
		}
	}

	asset, err := s.Create(ctx, CreateInput{
		Kind:                 kind,
		Name:                 displayName,
		ContentAddressedPath: key,
		OriginalFormat:       supported.Format,
		MetadataJSON:         metadata,
		Tags:                 []string{},
		DominantColor:        dominantColor,
		CreatedBy:            designerID,
	})
	if err != nil {
		if errors.Is(err, ErrNameInUse) {
			altName := uniquify(displayName, key)
			asset, err = s.Create(ctx, CreateInput{
				Kind:                 kind,
				Name:                 altName,
				ContentAddressedPath: key,
				OriginalFormat:       supported.Format,
				MetadataJSON:         metadata,
				Tags:                 []string{},
				DominantColor:        dominantColor,
				CreatedBy:            designerID,
			})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// Persist parsed animations once the asset row exists. Failures
	// here downgrade to a structured warn-log: the asset is already
	// usable (renderer falls back to frame 0), and a subsequent
	// re-import or designer-driven edit can backfill rows. We do
	// NOT roll back the asset row — half-imported sheets are still
	// preferable to a hard upload failure.
	if spriteImport != nil && len(spriteImport.Animations) > 0 {
		if err := s.ReplaceAnimations(ctx, asset.ID, spriteImport.Animations); err != nil {
			slog.Warn("upload: persist animations",
				"asset_id", asset.ID, "name", asset.Name, "err", err)
		}
	}

	return &UploadResult{
		Asset: asset, Reused: false, OriginalFn: originalName,
		TileCells: tileCells, TileMeta: tileMD, SpriteImport: spriteImport,
	}, nil
}

// MaxFilesPerUpload caps how many files a single multi-file upload
// request may carry. Sane upper bound for "drag a folder of tiles in";
// designers with larger sets will run multiple uploads.
const MaxFilesPerUpload = 64

// MultiUploadResult is the per-file outcome from UploadMany. Either
// Asset is populated (success) or Err is set (this single file failed
// — others in the batch may have succeeded).
//
// TileCells is populated when the resulting asset is kind=tile: it
// lists every 32x32 sub-cell of the sheet (with NonEmpty true for
// cells that contain at least one opaque pixel). The handler uses it
// to fan out one entity_type per non-empty cell so a tile sheet
// becomes paintable in the Mapmaker palette without any extra step.
// Empty for sprite/audio assets and for tile-sheet uploads that
// failed slicing (e.g. odd dimensions — the asset row is not
// created in that case; Err carries the reason).
type MultiUploadResult struct {
	Asset        *Asset
	Reused       bool
	OriginalFn   string
	Err          error
	TileCells    []TileCell
	TileMeta     TileSheetMetadata
	SpriteImport *ImportResult
}

// isAutoTileSheet returns true for plain PNG uploads that should default to
// tile-sheet slicing: any valid, non-empty, 32px-divisible grid larger than a
// single cell. Explicit Sprite/Sprite sheet/Animated sprite selections bypass
// this and stay sprite assets.
func isAutoTileSheet(body []byte) bool {
	cells, _, err := SliceTileSheet(body)
	if err != nil || len(cells) <= 1 {
		return false
	}
	return true
}

// SpriteSheetSummary describes the persisted frame metadata for a sprite
// upload. Handlers use it for upload-result captions and JSON responses.
type SpriteSheetSummary struct {
	Frames     int    `json:"frames"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
	GridW      int    `json:"grid_w"`
	GridH      int    `json:"grid_h"`
	Source     string `json:"source"`
	Animations int    `json:"animations"`
}

// SpriteSummaryFromImport converts an upload-time import result into the
// small presentation shape users need in the upload summary.
func SpriteSummaryFromImport(ir *ImportResult) SpriteSheetSummary {
	if ir == nil {
		return SpriteSheetSummary{}
	}
	md := ir.SheetMetadata
	frames := md.FrameCount
	if frames == 0 {
		frames = len(ir.Frames)
	}
	return SpriteSheetSummary{
		Frames:     frames,
		Cols:       md.Cols,
		Rows:       md.Rows,
		GridW:      md.GridW,
		GridH:      md.GridH,
		Source:     md.Source,
		Animations: len(ir.Animations),
	}
}

// SpriteSummaryFromMetadata decodes a previously persisted sprite sheet's
// metadata. It returns a zero summary for ordinary single-frame sprites.
func SpriteSummaryFromMetadata(raw []byte) SpriteSheetSummary {
	var md SheetMetadata
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return SpriteSheetSummary{}
	}
	if err := json.Unmarshal(raw, &md); err != nil {
		return SpriteSheetSummary{}
	}
	return SpriteSheetSummary{
		Frames: md.FrameCount,
		Cols:   md.Cols,
		Rows:   md.Rows,
		GridW:  md.GridW,
		GridH:  md.GridH,
		Source: md.Source,
	}
}

func (s SpriteSheetSummary) IsSheet() bool {
	return s.Frames > 1 || s.Cols > 1 || s.Rows > 1
}

func (s SpriteSheetSummary) String() string {
	if !s.IsSheet() {
		return ""
	}
	out := strconv.Itoa(s.Frames) + " frames"
	if s.Animations > 0 {
		out += " · " + strconv.Itoa(s.Animations) + " animations"
	}
	return out
}

// readSidecar locates and reads an Aseprite-style JSON sidecar from
// `siblings` whose base name matches `header`. Aseprite's "Export
// Sprite Sheet" UI produces "<base>.png" + "<base>.json" by default,
// so when both files arrive in the same multi-file upload we can wire
// the sidecar through automatically — designers don't need to flip
// the parser-override dropdown.
//
// Returns nil and no error when no sidecar exists. Read errors are
// non-fatal: missing/unreadable sidecar simply means we fall back to
// auto-slicing.
func readSidecar(header *multipart.FileHeader, siblings []*multipart.FileHeader) []byte {
	if header == nil {
		return nil
	}
	target := strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename)) + ".json"
	for _, sib := range siblings {
		if sib == nil || sib == header {
			continue
		}
		if !strings.EqualFold(filepath.Base(sib.Filename), target) {
			continue
		}
		f, err := sib.Open()
		if err != nil {
			return nil
		}
		body, err := io.ReadAll(io.LimitReader(f, MaxUploadBytes+1))
		_ = f.Close()
		if err != nil || len(body) == 0 || len(body) > MaxUploadBytes {
			return nil
		}
		return body
	}
	return nil
}

// normalizeContentType strips charset/parameters off DetectContentType output
// (e.g. "image/png; charset=utf-8") so the lookup table stays simple.
func normalizeContentType(ct string) string {
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// defaultDisplayName produces a designer-friendly default from a filename.
// "BossSheet_v3.png" -> "BossSheet_v3".
func defaultDisplayName(filename string) string {
	base := filepath.Base(filename)
	if base == "" || base == "." {
		return "untitled"
	}
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return "untitled"
	}
	return base
}

// uniquify appends a short suffix from the content key when (kind, name)
// collides on insert. Eight chars of hex is plenty to disambiguate without
// being unwieldy.
func uniquify(base, contentKey string) string {
	const tailLen = 8
	tail := contentKey
	if len(tail) > tailLen {
		tail = tail[len(tail)-tailLen:]
	}
	return base + " (" + tail + ")"
}
