// Package sse escreve Server-Sent Events com flush a cada mensagem e heartbeat (usado pela API e pelo painel).
package sse

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Writer serializa as escritas do SSE (eventos + heartbeat) e faz flush a cada mensagem.
type Writer struct {
	mu   sync.Mutex
	w    http.ResponseWriter
	f    http.Flusher
	stop chan struct{}
}

// Start devolve nil se o ResponseWriter não suporta streaming.
func Start(w http.ResponseWriter) *Writer {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	s := &Writer{w: w, f: f, stop: make(chan struct{})}
	s.Comment("conectado")
	// heartbeat: builds silenciosos não podem deixar o túnel/proxy fechar a conexão por ociosidade
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.Comment("ping")
			}
		}
	}()
	return s
}

func (s *Writer) Comment(c string) {
	s.mu.Lock()
	fmt.Fprintf(s.w, ": %s\n\n", c)
	s.f.Flush()
	s.mu.Unlock()
}

// Event envia um evento; id vazio não envia `id:`. data não pode conter \n (use JSON).
func (s *Writer) Event(name, id, data string) {
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

func (s *Writer) Close() { close(s.stop) }
