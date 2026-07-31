// Package migrations embeds the versioned SQL migration files into the
// binary so `cmd/migrate` can run them without the .sql files being present
// on disk at runtime. This is what lets the migrate step ship as a single
// self-contained container image (see deployments/docker/Dockerfile) with
// no volume-mounted SQL and no drift between the code and the migrations it
// runs.
//
// File naming follows golang-migrate's convention exactly:
//
//	NNNNNN_description.up.sql   — applied on `migrate up`
//	NNNNNN_description.down.sql — applied on `migrate down`
//
// The numeric prefix is the version; migrations run in ascending order.
// Never edit or renumber a migration that has already been applied to any
// shared environment — add a new one instead.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
