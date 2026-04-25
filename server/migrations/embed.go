// Package migrations embeds the SQL migration files so the production
// binary is self-contained. The persistence package consumes FS to drive
// golang-migrate.
package migrations

import "embed"

//go:embed all:*.sql
var FS embed.FS
