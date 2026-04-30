package designer

import (
	"net/http"

	"boxland/server/views"
)

func getPixiUIGallery(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		layout := BuildChrome(r, d)
		layout.Title = "Pixi UI Gallery"
		layout.Surface = "pixi-ui-gallery"
		layout.ActiveKind = "home"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{
			{Label: "Workspace", Href: "/design/"},
			{Label: "Pixi UI Gallery"},
		}
		renderHTML(w, r, views.PixiUIGalleryPage(views.PixiUIGalleryProps{Layout: layout}))
	}
}
