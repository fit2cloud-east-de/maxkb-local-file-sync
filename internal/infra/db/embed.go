package db

import "embed"

// MigrationsFS contains the versioned SQLite migrations shipped with the
// application. Keeping the files inside the Go package makes production startup
// independent of the source checkout and prevents an upgrade from silently
// falling back to the test-only InitSchema path.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
