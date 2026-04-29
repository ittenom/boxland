// Boxland — @pixi/layout side-effect import.
//
// `@pixi/layout` registers Pixi mixins on import; the entire
// codebase needs to share one registration so layout objects
// created here interop with `BoxlandApp.scene`. Import this file
// once at the top of any module that uses `.layout = {...}` on a
// Pixi `Container`.
//
// This is the standard `@pixi/layout` integration pattern from
// the upstream README. We isolate the side-effect import in this
// dedicated file so reviewers see it immediately and the rest of
// the editor-harness doesn't have a side-effecting top-level
// import surprise.

import "@pixi/layout";
