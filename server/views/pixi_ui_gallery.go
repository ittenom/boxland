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
			_, err := io.WriteString(w, `<main data-bx-pixi-ui-gallery style="display:block; width:100%; height:100%; min-height: 520px;"></main>
<script type="module" src="/static/web/pixi-ui-gallery.js" defer></script>`)
			return err
		})
		return Layout(p.Layout).Render(templ.WithChildren(ctx, child), w)
	})
}
