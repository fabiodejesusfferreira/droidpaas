// Package web embute os templates e os assets estáticos do painel (sem CDN; htmx vendorizado).
package web

import "embed"

//go:embed templates static
var FS embed.FS
