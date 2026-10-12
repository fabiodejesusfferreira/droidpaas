package deploy

import (
	"bytes"
	"io"
	"os"
	"sync"
)

// stream é o log de um deploy em andamento: grava no arquivo e repassa cada linha completa
// a quem está acompanhando (git push, SSE). O contador `written` permite reler o arquivo até o ponto
// exato da inscrição sem perder nem duplicar linhas.
type stream struct {
	mu      sync.Mutex
	f       *os.File
	written int64
	partial []byte
	subs    map[chan string]struct{}
	done    bool
}

const subBuffer = 1024

func newStream(path string) (*stream, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &stream{f: f, subs: map[chan string]struct{}{}}, nil
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.f.Write(p)
	s.written += int64(n)
	s.partial = append(s.partial, p[:n]...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(s.partial[:i], "\r"))
		s.partial = s.partial[i+1:]
		for ch := range s.subs {
			select {
			case ch <- line:
			default: // consumidor lento: desconecta (ele retoma pelo arquivo)
				close(ch)
				delete(s.subs, ch)
			}
		}
	}
	return n, err
}

func (s *stream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.partial) > 0 { // última linha sem \n
		line := string(s.partial)
		for ch := range s.subs {
			select {
			case ch <- line:
			default:
			}
		}
		s.partial = nil
	}
	s.done = true
	s.f.Close()
	for ch := range s.subs {
		close(ch)
		delete(s.subs, ch)
	}
}

// subscribe devolve quantos bytes do arquivo já foram gravados (replay) e o canal das linhas seguintes.
// Se o stream já terminou, o canal vem fechado.
func (s *stream) subscribe() (replayBytes int64, ch chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch = make(chan string, subBuffer)
	if s.done {
		close(ch)
		return s.written, ch
	}
	s.subs[ch] = struct{}{}
	return s.written, ch
}

func (s *stream) unsubscribe(ch chan string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
}

// Hub guarda os streams dos deploys em andamento.
type Hub struct {
	mu sync.Mutex
	m  map[int64]*stream
}

func newHub() *Hub { return &Hub{m: map[int64]*stream{}} }

func (h *Hub) open(id int64, path string) (*stream, error) {
	s, err := newStream(path)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.m[id] = s
	h.mu.Unlock()
	return s, nil
}

func (h *Hub) finish(id int64) {
	h.mu.Lock()
	s := h.m[id]
	delete(h.m, id)
	h.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (h *Hub) get(id int64) *stream {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.m[id]
}

// readN lê os n primeiros bytes de um arquivo de log.
func readN(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}
