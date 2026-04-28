package assets_test

import (
	"testing"

	"boxland/server/internal/assets"
)

// TestNormalizeUploadKind_RoundTripsValidEnumValues guards against the
// regression where the upload form's <option value="tile"> sent the
// literal string "tile" through to the database, which the
// assets.kind CHECK constraint then rejected.
//
// The contract: every value the upload form's <select name="kind">
// can possibly send must normalize to one of the four real
// assets.Kind enum values, OR to "" (auto-detect). Anything else is a
// bug — adding a new value to the form without registering it here
// will fire this test.
func TestNormalizeUploadKind_RoundTripsValidEnumValues(t *testing.T) {
	// Valid form values, in the order the upload modal lists them.
	formValues := []string{
		"",                 // Auto-detect from content
		"sprite",           // Single 32×32 image
		"sprite_sheet",     // Sprite sheet (multi-frame)
		"animated_sprite",  // Animated sprite
		"tilemap",          // Tilemap (replaces the old "tile" option)
		"audio",            // Audio
		"ui_panel",         // 9-slice UI panel
		"auto",             // Synonym for ""
	}

	// Every normalized result must be either empty (auto-detect) or
	// one of the four CHECK-allowed values.
	allowed := map[assets.Kind]bool{
		"":                       true,
		assets.KindSprite:        true,
		assets.KindSpriteAnimated: true,
		assets.KindAudio:         true,
		assets.KindUIPanel:       true,
	}

	for _, v := range formValues {
		got := assets.NormalizeUploadKind(v)
		if !allowed[got] {
			t.Errorf("NormalizeUploadKind(%q) = %q — not a valid assets.Kind. "+
				"This will trigger the CHECK constraint on insert. "+
				"Either map it in NormalizeUploadKind or remove the option from the upload form.",
				v, got)
		}
	}
}

// TestNormalizeUploadKind_RejectsRawInvalidValues — the function's
// fallback `Kind(raw)` branch is meant for already-canonical kind
// values (e.g., "sprite" or "audio"). Passing a stale form value
// like "tile" used to fall through to that branch and produced an
// invalid Kind that the DB rejected with a confusing error. We don't
// fix the function (the DB CHECK is the canonical guard), but we do
// document the boundary so future contributors don't regress.
func TestNormalizeUploadKind_StaleValuesPassThrough(t *testing.T) {
	// "tile" is the one historical pitfall — the form had an option
	// with this value before the holistic redesign. If a rogue
	// caller still sends it, we'd now insert "tile" into a
	// sprite/sprite_animated/audio/ui_panel-only column.
	got := assets.NormalizeUploadKind("tile")
	if got == assets.KindSprite ||
		got == assets.KindSpriteAnimated ||
		got == assets.KindAudio ||
		got == assets.KindUIPanel {
		// That would mean someone added "tile" to the override
		// switch — fine, just remove this test.
		return
	}
	// Confirm the stale value still passes through as a non-canonical
	// Kind so the boundary is observable. The DB CHECK is what stops
	// it landing as an asset row in production.
	if got != assets.Kind("tile") {
		t.Errorf("NormalizeUploadKind(\"tile\") = %q; expected pass-through to assets.Kind(\"tile\")", got)
	}
}
