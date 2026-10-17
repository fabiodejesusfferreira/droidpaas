package web

import (
	"fmt"
	"html/template"
	"time"
)

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"t":  T,
		"tr": TR,
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: número ímpar de argumentos")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: chave não é string")
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
		"shortSHA": shortSHA,
		"humanKB":  humanKB,
		"humanB":   humanBytes,
		"agoT":     func(t time.Time) string { return agoSince(s.Now(), t) },
		"agoMS": func(ms int64) string {
			if ms == 0 {
				return "—"
			}
			return agoSince(s.Now(), time.UnixMilli(ms))
		},
		"idx": formIndex,
		"add": func(a, b int) int { return a + b },
	}
}
