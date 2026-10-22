// Package platform holds the migrations for the platform's own tables (accounts, sites, audit log, publish jobs).
// Each user's website has its own schema, built from the migrations in the parent folder.
package platform

import "embed"

//go:embed *.sql
var FS embed.FS
