package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"droidpaas/internal/deploy"
	"droidpaas/internal/store"
)

type deployView struct {
	store.Deploy
	App string `json:"app"`
}

func (s *Server) deployErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deploy.ErrBadRef):
		fail(w, 409, "bad_ref", err.Error())
	case errors.Is(err, deploy.ErrNoPrevious):
		fail(w, 409, "no_previous_release", err.Error())
	case errors.Is(err, deploy.ErrBadRelease):
		fail(w, 422, "bad_release", err.Error())
	case errors.Is(err, deploy.ErrNotActive):
		fail(w, 409, "not_active", err.Error())
	case errors.Is(err, deploy.ErrTooLate):
		fail(w, 409, "too_late", err.Error())
	default:
		s.appErr(w, err)
	}
}

func (s *Server) registerDeploys(mux *http.ServeMux) {
	appOf := func(w http.ResponseWriter, r *http.Request) (store.App, bool) {
		a, err := s.Apps.Get(r.Context(), r.PathValue("name"))
		if err != nil {
			s.appErr(w, err)
			return a, false
		}
		return a, true
	}
	deployOf := func(w http.ResponseWriter, r *http.Request) (store.Deploy, string, bool) {
		id, ok := pathID(r)
		if !ok {
			fail(w, 400, "bad_request", "id inválido")
			return store.Deploy{}, "", false
		}
		d, err := s.Store.Deploy(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fail(w, 404, "deploy_not_found", "deploy inexistente")
				return d, "", false
			}
			s.internalErr(w, err)
			return d, "", false
		}
		a, err := s.Store.AppByID(r.Context(), d.AppID)
		if err != nil {
			s.internalErr(w, err)
			return d, "", false
		}
		return d, a.Name, true
	}

	mux.HandleFunc("GET /api/v1/apps/{name}/deploys", s.full(func(w http.ResponseWriter, r *http.Request) {
		a, ok := appOf(w, r)
		if !ok {
			return
		}
		limit, before := page(r)
		ds, err := s.Store.Deploys(r.Context(), a.ID, limit, before)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		out := make([]deployView, len(ds))
		for i, d := range ds {
			out[i] = deployView{d, a.Name}
		}
		writeJSON(w, 200, out)
	}))
	mux.HandleFunc("POST /api/v1/apps/{name}/deploys", s.full(func(w http.ResponseWriter, r *http.Request) {
		a, ok := appOf(w, r)
		if !ok {
			return
		}
		var in struct {
			Ref     string `json:"ref"`
			Rebuild bool   `json:"rebuild"`
		}
		if !decode(w, r, &in) {
			return
		}
		trigger := "manual"
		if in.Rebuild {
			trigger = "rebuild"
		}
		d, err := s.Deploy.Enqueue(r.Context(), a.Name, trigger, deploy.EnqueueOpts{Ref: in.Ref})
		if err != nil {
			s.deployErr(w, err)
			return
		}
		writeJSON(w, 202, deployView{d, a.Name})
	}))
	mux.HandleFunc("POST /api/v1/apps/{name}/rollback", s.full(func(w http.ResponseWriter, r *http.Request) {
		a, ok := appOf(w, r)
		if !ok {
			return
		}
		var in struct {
			ReleaseID int64 `json:"release_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		d, err := s.Deploy.Enqueue(r.Context(), a.Name, "rollback", deploy.EnqueueOpts{ReleaseID: in.ReleaseID})
		if err != nil {
			s.deployErr(w, err)
			return
		}
		writeJSON(w, 202, deployView{d, a.Name})
	}))
	mux.HandleFunc("GET /api/v1/apps/{name}/releases", s.full(func(w http.ResponseWriter, r *http.Request) {
		a, ok := appOf(w, r)
		if !ok {
			return
		}
		rels, err := s.Store.Releases(r.Context(), a.ID, r.URL.Query().Get("status"))
		if err != nil {
			s.internalErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"releases": rels, "current_release_id": a.CurrentReleaseID})
	}))
	mux.HandleFunc("GET /api/v1/deploys/{id}", s.full(func(w http.ResponseWriter, r *http.Request) {
		if d, app, ok := deployOf(w, r); ok {
			writeJSON(w, 200, deployView{d, app})
		}
	}))
	mux.HandleFunc("POST /api/v1/deploys/{id}/cancel", s.full(func(w http.ResponseWriter, r *http.Request) {
		d, _, ok := deployOf(w, r)
		if !ok {
			return
		}
		if err := s.Deploy.Cancel(r.Context(), d.ID); err != nil {
			s.deployErr(w, err)
			return
		}
		w.WriteHeader(202)
	}))
	mux.HandleFunc("GET /api/v1/deploys/{id}/logs", s.full(func(w http.ResponseWriter, r *http.Request) {
		d, _, ok := deployOf(w, r)
		if !ok {
			return
		}
		wantSSE := r.URL.Query().Get("follow") == "1" || r.Header.Get("Accept") == "text/event-stream"
		if !wantSSE { // texto simples: o que já foi gravado
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if f, err := os.Open(d.LogPath); err == nil {
				defer f.Close()
				io.Copy(w, f)
			}
			return
		}
		from, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
		if n, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil {
			from = n
		}
		sse := startSSE(w)
		if sse == nil {
			fail(w, 500, "internal", "streaming não suportado")
			return
		}
		defer sse.close()
		status, err := s.Deploy.Follow(r.Context(), d.ID, from, func(n int, line string) {
			b, _ := json.Marshal(map[string]any{"n": n, "text": line})
			sse.event("line", strconv.Itoa(n), string(b))
		})
		if err != nil {
			return // cliente saiu
		}
		b, _ := json.Marshal(map[string]string{"status": status})
		sse.event("end", "", string(b))
	}))
}
