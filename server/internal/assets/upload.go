package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
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
}

// SupportedContentTypes maps the sniffed MIME type to the kind it produces.
// We use sniffing (not the client-supplied type) because the client may lie
// or guess wrong. Anything outside this set is rejected with 415.
var SupportedContentTypes = map[string]struct {
	Kind   Kind
	Format string
}{
	"image/png":  {KindSprite, "png"},
	"audio/wav":  {KindAudio, "wav"},
	"audio/x-wav": {KindAudio, "wav"},
	"audio/wave": {KindAudio, "wav"},
	"audio/ogg":  {KindAudio, "ogg"},
	"audio/mpeg": {KindAudio, "mp3"},
}

// Errors returned by Upload. Stable for HTTP handler mapping.
var (
	ErrNoFile                = errors.New("upload: no file in request")
	ErrTooLarge              = errors.New("upload: file exceeds size limit")
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
	file, header, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, ErrNoFile
		}
		return nil, fmt.Errorf("read file: %w", err)
	}
	defer file.Close()

	// Read into memory once. We need it for both the sha256 (via the
	// content-addressed key helper) and the upload body.
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
		kind = kindOverride
	}
	originalName := header.Filename
	displayName := defaultDisplayName(originalName)

	key := persistence.ContentAddressedKey("assets", body)

	// Dedup: if an asset of this kind with this exact content already
	// exists, return it untouched. The caller decides whether to surface
	// "Reused: true" to the user.
	if existing, err := s.FindByContentPath(ctx, kind, key); err == nil {
		return &UploadResult{Asset: existing, Reused: true, OriginalFn: originalName}, nil
	} else if !errors.Is(err, ErrAssetNotFound) {
		return nil, fmt.Errorf("dedup lookup: %w", err)
	}

	// Push to object storage. Content-addressed: PutObject on identical
	// bytes is idempotent; we re-upload anyway to be safe (no extra
	// HeadObject round-trip), and the cost is one stream of bytes.
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
	}

	asset, err := s.Create(ctx, CreateInput{
		Kind:                 kind,
		Name:                 displayName,
		ContentAddressedPath: key,
		OriginalFormat:       supported.Format,
		MetadataJSON:         metadata,
		Tags:                 []string{},
		CreatedBy:            designerID,
	})
	if err != nil {
		// If the (kind, name) collided we fall back to a uniquified name so
		// the upload still succeeds. The designer can rename later.
		if errors.Is(err, ErrNameInUse) {
			altName := uniquify(displayName, key)
			asset, err = s.Create(ctx, CreateInput{
				Kind:                 kind,
				Name:                 altName,
				ContentAddressedPath: key,
				OriginalFormat:       supported.Format,
				MetadataJSON:         metadata,
				Tags:                 []string{},
				CreatedBy:            designerID,
			})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return &UploadResult{Asset: asset, Reused: false, OriginalFn: originalName}, nil
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
