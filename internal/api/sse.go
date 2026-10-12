package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// sseWriter serializa as escritas do SSE (eventos + heartbeat) e faz flush a cada mensagem.
type sseWriter struct {
	mu   sync.Mutex
	w    http.ResponseWriter
	f    http.Flusher
	stop chan struct{}
}

// startSSE devolve nil se o ResponseWriter não suporta streaming.
func startSSE(w http.ResponseWriter) *sseWriter {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	s := &sseWriter{w: w, f: f, stop: make(chan struct{})}
	s.comment("conectado")
	// heartbeat: builds silenciosos não podem deixar o túnel/proxy fechar a conexão por ociosidade
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.comment("ping")
			}
		}
	}()
	return s
}

func (s *sseWriter) comment(c string) {
	s.mu.Lock()
	fmt.Fprintf(s.w, ": %s\n\n", c)
	s.f.Flush()
	s.mu.Unlock()
}

// event envia um evento; id vazio não envia `id:`. data não pode conter \n (use JSON).
func (s *sseWriter) event(name, id, data string) {
	s.mu.Lock()
	var b strings.Builder
	if id != "" {
		fmt.Fprintf(&b, "id: %s\n", id)
	}
	fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, data)
	fmt.Fprint(s.w, b.String())
	s.f.Flush()
	s.mu.Unlock()
}

func (s *sseWriter) close() { close(s.stop) }
