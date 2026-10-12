package api

import (
	"fmt"
	"net/http"

	"droidpaas/internal/gitsrv"
)

// handleHook recebe o post-receive pelo socket Unix, enfileira o deploy e devolve o log em streaming;
// o código de saída vai no trailer X-Droid-Exit (TSD §8.4.4, §10.4).
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	var in struct {
		App     string          `json:"app"`
		Updates []gitsrv.Update `json:"updates"`
	}
	if !decode(w, r, &in) {
		return
	}
	a, err := s.Apps.Get(r.Context(), in.App)
	if err != nil {
		http.Error(w, "app inexistente", http.StatusNotFound)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Trailer", "X-Droid-Exit")
	fl, _ := w.(http.Flusher)
	say := func(format string, a ...any) {
		fmt.Fprintf(w, format+"\n", a...)
		if fl != nil {
			fl.Flush()
		}
	}
	done := func(code int) { h.Set("X-Droid-Exit", fmt.Sprint(code)) }

	var match *gitsrv.Update
	for i, u := range in.Updates {
		if u.Ref == "refs/heads/"+a.Branch && u.New != gitsrv.ZeroSHA {
			match = &in.Updates[i]
		}
	}
	if match == nil {
		say("DroidPaaS: push recebido; só o branch %q dispara deploy neste app.", a.Branch)
		done(0)
		return
	}
	d, err := s.Deploy.Enqueue(r.Context(), a.Name, "git_push", deployOpts(match))
	if err != nil {
		say("DroidPaaS: não consegui enfileirar o deploy: %v", err)
		done(1)
		return
	}
	say("DroidPaaS: deploy #%d na fila (commit %s)", d.ID, match.New[:7])
	status, err := s.Deploy.Follow(r.Context(), d.ID, 0, func(_ int, line string) { say("%s", line) })
	if err != nil { // o cliente saiu (Ctrl+C no push): o deploy segue em segundo plano
		return
	}
	if status == "succeeded" {
		done(0)
		return
	}
	say("DroidPaaS: o deploy terminou com status %q; a release anterior segue no ar.", status)
	done(1)
}
