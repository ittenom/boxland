package views_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"boxland/server/internal/auth/csrf"
	"boxland/server/views"
)

func TestLayout_Renders(t *testing.T) {
	// Inject a fake CSRF token via context.
	ctx := context.Background()
	// csrf.Token uses a private key; the layout calls csrf.Token(ctx) and
	// prints whatever it returns. With no middleware run, the token will be
	// empty — that's fine for this rendering test.
	_ = csrf.Token

	var buf bytes.Buffer
	err := views.Layout(views.LayoutProps{
		Title:   "Test",
		Surface: "test",
	}).Render(ctx, &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"<!doctype html>",
		`<title>Test`,
		`data-surface="test"`,
		`/static/css/pixel.css`,
		`<meta name="csrf-token"`,
	} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("missing in output: %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestLayout_HideTopNav(t *testing.T) {
	var buf bytes.Buffer
	err := views.Layout(views.LayoutProps{
		Title:      "Login",
		Surface:    "auth",
		HideTopNav: true,
	}).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(buf.String(), "Push to Live") {
		t.Error("HideTopNav=true should suppress the top nav")
	}
}
