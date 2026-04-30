package views

import (
	"context"
	"io"

	"github.com/a-h/templ"
)

type PixiUIGalleryProps struct {
	Layout LayoutProps
}

func PixiUIGalleryPage(p PixiUIGalleryProps) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		child := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := io.WriteString(w, `<section class="bx-stack" style="height: 100%; min-height: 0;">
	<header class="bx-row bx-row--apart">
		<div>
			<h1 style="margin:0;">Pixi UI Gallery</h1>
			<p class="bx-muted bx-small" style="margin:6px 0 0;">Canonical editor components rendered with Pixi.</p>
		</div>
		<a class="bx-btn bx-btn--ghost bx-btn--small" href="/design/maps">Back to maps</a>
	</header>
	<main data-bx-pixi-ui-gallery style="flex:1; min-height: 520px; border: 1px solid var(--bx-line); background: var(--bx-bg-0);"></main>
	<script type="module" src="/static/web/pixi-ui-gallery.js" defer></script>
</section>`)
			return err
		})
		return Layout(p.Layout).Render(templ.WithChildren(ctx, child), w)
	})
}
