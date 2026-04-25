// Package static embeds the design-tool static assets (CSS, fonts,
// vendor JS, icons, compiled web bundle) into the server binary so the
// production image is self-contained.
//
// Files appear under their on-disk paths; FS is intended to be passed to
// http.FileServer or templ asset helpers.
//
// `web/` holds the Vite-built TS bundle (entry-game.ts → game.js, etc.).
// The justfile's `_stage-web` recipe copies web/dist into static/web
// before each Go run, and the production Docker image does the same
// copy in its multi-stage build. The directory is gitignored; a
// .gitkeep keeps the embed directive resolvable when nothing has been
// staged yet (an empty FS sub-tree, not a build error).
package static

import "embed"

//go:embed all:css all:fonts all:js all:vendor all:icons all:web
var FS embed.FS
