// Package proxy gera e aplica a config do Caddy (TSD §8.5). Implementação na Fase 2D.
package proxy

import "context"

// ProxyClient fala com a Admin API do Caddy.
type ProxyClient interface {
	// Load aplica a config completa (POST /load).
	Load(ctx context.Context, cfg []byte) error
}
