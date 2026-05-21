// Package migrations holds SQL shipped in the binary via go:embed.
//
// Workflow: edit only *.sql files in this directory (e.g. 001_schema.sql,
// 002_data.sql). The //go:embed directive includes every .sql here at compile
// time—no separate copy step. After changing SQL, rebuild the server so the
// new files are embedded. Runtime migration order is fixed in internal/migrate.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
