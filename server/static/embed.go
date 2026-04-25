// Package static embeds the design-tool static assets (CSS, fonts,
// vendor JS, icons, compiled web bundle) into the server binary so the
// production image is self-contained.
//
// Files appear under their on-disk paths; FS is intended to be passed to
// http.FileServer or templ asset helpers.
package static

import "embed"

//go:embed all:css all:fonts all:js all:vendor all:icons
var FS embed.FS
