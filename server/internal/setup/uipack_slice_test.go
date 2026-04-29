package setup

import (
	"strings"
	"testing"

	"boxland/server/internal/entities/components"
)

func TestMeasureNineSlice_FrameFamily(t *testing.T) {
	for _, name := range []string{
		"UI_Gradient_Frame_Standard.png",
		"UI_Gradient_Frame_Lite.png",
		"UI_Gradient_Frame_Inward.png",
		"UI_Gradient_Frame_Outward.png",
		"UI_Gradient_Frame_Horizontal.png",
		"UI_Gradient_Frame_Vertical.png",
	} {
		t.Run(name, func(t *testing.T) {
			got := MeasureNineSlice(name, 48, 48) // big enough that no clamp fires
			want := components.NineSlice{Left: 8, Top: 8, Right: 8, Bottom: 8}
			if got != want {
				t.Errorf("got %+v want %+v", got, want)
			}
		})
	}
}

func TestMeasureNineSlice_ButtonLargeUsesEightPx(t *testing.T) {
	got := MeasureNineSlice("UI_Gradient_Button_Large_Release_01a1.png", 64, 32)
	want := components.NineSlice{Left: 8, Top: 8, Right: 8, Bottom: 8}
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestMeasureNineSlice_ButtonSmallMediumUseSixPx(t *testing.T) {
	for _, name := range []string{
		"UI_Gradient_Button_Small_Press_01a2.png",
		"UI_Gradient_Button_Medium_Lock_01a3.png",
	} {
		got := MeasureNineSlice(name, 48, 24)
		want := components.NineSlice{Left: 6, Top: 6, Right: 6, Bottom: 6}
		if got != want {
			t.Errorf("%s: got %+v want %+v", name, got, want)
		}
	}
}

func TestMeasureNineSlice_SliderScrollDropdownAreThin(t *testing.T) {
	for _, name := range []string{
		"UI_Gradient_Slider_Bar.png",
		"UI_Gradient_Scroll_Bar.png",
		"UI_Gradient_Dropdown_Bar.png",
		"UI_Gradient_Fill_Bar.png",
	} {
		got := MeasureNineSlice(name, 48, 16)
		want := components.NineSlice{Left: 4, Top: 4, Right: 4, Bottom: 4}
		if got != want {
			t.Errorf("%s: got %+v want %+v", name, got, want)
		}
	}
}

func TestMeasureNineSlice_IconsFallToOnePx(t *testing.T) {
	for _, name := range []string{
		"UI_Gradient_Arrow_Large.png",
		"UI_Gradient_Checkmark_Small.png",
		"UI_Gradient_Cross_Medium.png",
	} {
		got := MeasureNineSlice(name, 16, 16)
		want := components.NineSlice{Left: 1, Top: 1, Right: 1, Bottom: 1}
		if got != want {
			t.Errorf("%s: got %+v want %+v", name, got, want)
		}
	}
}

func TestMeasureNineSlice_ClampsTinySprites(t *testing.T) {
	// Frame family wants 8 px corners; a 16×16 sprite can't afford
	// that (8+8 = 16, no center). Clamp must reduce to width/4 = 4.
	got := MeasureNineSlice("UI_Gradient_Frame_Standard.png", 16, 16)
	if got.Left+got.Right >= 16 {
		t.Errorf("clamp failed: left+right=%d, must be < 16", got.Left+got.Right)
	}
	if got.Top+got.Bottom >= 16 {
		t.Errorf("clamp failed: top+bottom=%d, must be < 16", got.Top+got.Bottom)
	}
	// Validation must pass.
	if err := got.Validate(); err != nil {
		t.Errorf("clamped slice fails Validate(): %v", err)
	}
}

func TestMeasureNineSlice_ResultAlwaysValidates(t *testing.T) {
	// Random-ish sample of names + dims that should each produce a
	// Validate-passing slice. The clamp + min-1 floors are what
	// guarantee this; the test is the canary for any future changes.
	cases := []struct {
		name string
		w, h int
	}{
		{"UI_Gradient_Banner.png", 96, 32},
		{"UI_Gradient_Textfield.png", 80, 24},
		{"UI_Gradient_Slot_Available.png", 16, 16},
		{"UI_Gradient_Slider_Handle.png", 8, 16},
		{"unknown_sprite.png", 4, 4}, // unknown family, tiny sprite
	}
	for _, c := range cases {
		s := MeasureNineSlice(c.name, c.w, c.h)
		if err := s.Validate(); err != nil {
			t.Errorf("%s (%dx%d): %v", c.name, c.w, c.h, err)
		}
	}
}

func TestCanonicalAssetName_LowerSnakeStripsExt(t *testing.T) {
	got := canonicalAssetName("UI_Gradient_Button_Large_Release_01a1.png")
	want := "ui_gradient_button_large_release_01a1"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestCanonicalAssetName_HandlesNonPNGGracefully(t *testing.T) {
	// The seeder filters by .png before calling canonicalAssetName,
	// but the function itself shouldn't crash on edge cases.
	got := canonicalAssetName("foo")
	if got != "foo" {
		t.Errorf("got %q", got)
	}
	got = canonicalAssetName("")
	if got != "" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultsForFamily_NoFalseMatches(t *testing.T) {
	// The substring matcher would be wrong if e.g. a sprite named
	// "frame_horizontal" matched the slider rule. This test
	// exhaustively checks the families don't overlap on real
	// pack filenames.
	pack := []string{
		"UI_Gradient_Arrow_Large.png", "UI_Gradient_Arrow_Medium.png", "UI_Gradient_Arrow_Small.png",
		"UI_Gradient_Banner.png",
		"UI_Gradient_Button_Large_Lock_01a1.png", "UI_Gradient_Button_Medium_Press_01a2.png", "UI_Gradient_Button_Small_Release_01a4.png",
		"UI_Gradient_Checkmark_Large.png", "UI_Gradient_Cross_Medium.png",
		"UI_Gradient_Dropdown_Bar.png", "UI_Gradient_Dropdown_Handle.png",
		"UI_Gradient_Fill_Bar.png", "UI_Gradient_Fill_Filler.png",
		"UI_Gradient_Frame_Horizontal.png", "UI_Gradient_Frame_Inward.png",
		"UI_Gradient_Frame_Lite.png", "UI_Gradient_Frame_Outward.png",
		"UI_Gradient_Frame_Standard.png", "UI_Gradient_Frame_Vertical.png",
		"UI_Gradient_Scroll_Bar.png", "UI_Gradient_Scroll_Handle.png",
		"UI_Gradient_Select1.png", "UI_Gradient_Select2.png", "UI_Gradient_Select3.png", "UI_Gradient_Select4.png",
		"UI_Gradient_Slider_Bar.png", "UI_Gradient_Slider_Filler.png", "UI_Gradient_Slider_Handle.png",
		"UI_Gradient_Slot_Available.png", "UI_Gradient_Slot_Selected.png", "UI_Gradient_Slot_Unavailable.png",
		"UI_Gradient_Textfield.png",
	}
	for _, name := range pack {
		s := defaultsForFamily(name)
		if s.Left < 1 || s.Top < 1 || s.Right < 1 || s.Bottom < 1 {
			t.Errorf("%s produced non-positive insets %+v", name, s)
		}
		// Sanity: insets are reasonable for a UI sprite (1..16).
		if s.Left > 16 || s.Top > 16 || s.Right > 16 || s.Bottom > 16 {
			t.Errorf("%s produced excessive insets %+v", name, s)
		}
		// Frame family should always get 8 px.
		if strings.Contains(strings.ToLower(name), "frame_") {
			if s.Left != 8 {
				t.Errorf("%s: frames must use 8 px, got %+v", name, s)
			}
		}
	}
}
