// Package migrations embeds the golang-migrate SQL files so the API binary can
// apply migrations itself (`go run ./cmd/api migrate up`) with no CLI install.
package migrations

import "embed"

// FS holds the versioned migration files.
//
//go:embed *.sql
var FS embed.FS
