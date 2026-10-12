// Package migrations embute os arquivos SQL versionados (NNNN_nome.sql).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
