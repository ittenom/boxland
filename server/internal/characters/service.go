// Package characters owns the character-generator domain: slots, parts,
// recipes, bakes, stat sets, talent trees, NPC templates, and player
// characters.
//
// Lifecycle: designer-authored definitions (slots/parts/stat sets/talent
// trees/NPC templates) flow through the existing artifact publish
// pipeline (server/internal/publishing/artifact). Recipes and bakes
// are NOT artifacts; they're outputs of the publish step or of player
// edits. See docs/superpowers/plans/2026-04-26-character-generator-plan.md.
//
// Tenant isolation: every player route reads player_id from the
// authenticated context, never from the request body. Designer routes
// require a designer session.
//
// The Service constructor lives in repo.go; row types live in
// definitions.go.
package characters
