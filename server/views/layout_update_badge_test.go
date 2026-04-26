package views_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"boxland/server/views"
)

// The chrome update pill is the in-app surface of the new release
// notification. It must:
//
//   - render only when LayoutProps.UpdateBadge is non-nil
//   - link to the release URL in a new tab
//   - show both versions with leading `v` prefixes for consistency
func TestChrome_UpdateBadgeHiddenByDefault(t *testing.T) {
	var buf bytes.Buffer
	err := views.Chrome(views.LayoutProps{}).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "bx-chrome__update") {
		t.Errorf("update pill should be absent without UpdateBadge\n--- got ---\n%s", out)
	}
}

func TestChrome_UpdateBadgeRenders(t *testing.T) {
	var buf bytes.Buffer
	err := views.Chrome(views.LayoutProps{
		UpdateBadge: &views.UpdateBadge{
			Current:    "0.1.0",
			Latest:     "v0.2.0",
			ReleaseURL: "https://github.com/ittenom/boxland/releases/tag/v0.2.0",
		},
	}).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`bx-chrome__update`,
		`v0.1.0`,
		`v0.2.0`,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
		`https://github.com/ittenom/boxland/releases/tag/v0.2.0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("update pill missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestChrome_UpdateBadgeBareVersionGetsVPrefix(t *testing.T) {
	// Even when the upstream tag is bare ("0.2.0"), the rendered
	// pill should display "v0.2.0" so users see the same
	// convention everywhere.
	var buf bytes.Buffer
	_ = views.Chrome(views.LayoutProps{
		UpdateBadge: &views.UpdateBadge{
			Current: "0.1.0",
			Latest:  "0.2.0", // intentionally no `v`
		},
	}).Render(context.Background(), &buf)
	if !strings.Contains(buf.String(), "v0.2.0") {
		t.Errorf("bare latest version should be displayed with v prefix; got\n%s", buf.String())
	}
}
