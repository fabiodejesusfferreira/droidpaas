package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os/user"
	"strconv"
	"strings"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/logs"
	"droidpaas/internal/store"
)

type appView struct {
	store.App
	State string `json:"state"`
	// ResolvedRuntime é o runtime detectado na release ativa (o campo runtime pode ser "auto").
	ResolvedRuntime string   `json:"resolved_runtime"`
	Domains         []string `json:"domains"`
	Git             gitURLs  `json:"git"`
}

type gitURLs struct {
	SSH  string `json:"ssh,omitempty"`
	HTTP string `json:"http,omitempty"`
}

func (s *Server) view(r *http.Request, a store.App) (appView, error) {
	ctx := r.Context()
	ds, err := s.Store.Domains(ctx, a.ID)
	if err != nil {
		return appView{}, err
	}
	v := appView{App: a, State: s.Apps.State(ctx, a), Domains: []string{}}
	if a.CurrentReleaseID != nil {
		if rel, err := s.Store.Release(ctx, *a.CurrentReleaseID); err == nil {
			v.ResolvedRuntime = rel.Runtime
		}
	}
	for _, d := range ds {
		v.Domains = append(v.Domains, d.Hostname)
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if s.Cfg.Git.SSHEnabled {
		name := "git"
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		v.Git.SSH = "ssh://" + name + "@" + host + ":" + strconv.Itoa(s.Cfg.Git.SSHPort) + "/" + a.Name + ".git"
	}
	if set, _ := s.effectiveSettings(r); set["git_http_enabled"] == "true" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		v.Git.HTTP = scheme + "://" + r.Host + "/git/" + a.Name + ".git"
	}
	return v, nil
}

// appErr traduz erros de domínio para o formato de erro da API.
func (s *Server) appErr(w http.ResponseWriter, err error) {
	var ve *apps.ValidationError
	switch {
	case errors.As(err, &ve):
		failD(w, 422, "validation", ve.Error(), map[string]any{"field": ve.Field})
	case errors.Is(err, apps.ErrConflict), errors.Is(err, store.ErrExists):
		fail(w, 409, "conflict", "já existe")
	case errors.Is(err, apps.ErrNoRelease):
		fail(w, 409, "no_release", err.Error())
	case errors.Is(err, store.ErrNotFound):
		fail(w, 404, "app_not_found", "app inexistente")
	default:
		s.internalErr(w, err)
	}
}

func (s *Server) registerApps(mux *http.ServeMux) {
	one := func(h func(w http.ResponseWriter, r *http.Request, a store.App)) http.HandlerFunc {
		return s.full(func(w http.ResponseWriter, r *http.Request) {
			a, err := s.Apps.Get(r.Context(), r.PathValue("name"))
			if err != nil {
				s.appErr(w, err)
				return
			}
			h(w, r, a)
		})
	}
	reply := func(w http.ResponseWriter, r *http.Request, code int, a store.App) {
		v, err := s.view(r, a)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		writeJSON(w, code, v)
	}

	mux.HandleFunc("GET /api/v1/apps", s.full(func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Apps.List(r.Context())
		if err != nil {
			s.internalErr(w, err)
			return
		}
		out := make([]appView, 0, len(list))
		for _, a := range list {
			v, err := s.view(r, a)
			if err != nil {
				s.internalErr(w, err)
				return
			}
			out = append(out, v)
		}
		writeJSON(w, 200, out)
	}))
	mux.HandleFunc("POST /api/v1/apps", s.full(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string `json:"name"`
			Runtime string `json:"runtime"`
			Branch  string `json:"branch"`
		}
		if !decode(w, r, &in) {
			return
		}
		a, err := s.Apps.Create(r.Context(), apps.CreateInput{Name: in.Name, Runtime: in.Runtime, Branch: in.Branch})
		if err != nil {
			s.appErr(w, err)
			return
		}
		reply(w, r, 201, a)
	}))
	mux.HandleFunc("GET /api/v1/apps/{name}", one(func(w http.ResponseWriter, r *http.Request, a store.App) { reply(w, r, 200, a) }))
	mux.HandleFunc("PATCH /api/v1/apps/{name}", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		var p apps.Patch
		if !decode(w, r, &p) {
			return
		}
		a, err := s.Apps.Update(r.Context(), a.Name, p)
		if err != nil {
			s.appErr(w, err)
			return
		}
		reply(w, r, 200, a)
	}))
	mux.HandleFunc("DELETE /api/v1/apps/{name}", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		if err := s.Apps.Delete(r.Context(), a.Name, r.URL.Query().Get("confirm")); err != nil {
			s.appErr(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	for action, fn := range map[string]func(*apps.Service) func(context.Context, string) (store.App, error){
		"start":   func(a *apps.Service) func(context.Context, string) (store.App, error) { return a.Start },
		"stop":    func(a *apps.Service) func(context.Context, string) (store.App, error) { return a.Stop },
		"restart": func(a *apps.Service) func(context.Context, string) (store.App, error) { return a.Restart },
	} {
		do := fn(s.Apps)
		mux.HandleFunc("POST /api/v1/apps/{name}/"+action, one(func(w http.ResponseWriter, r *http.Request, a store.App) {
			a, err := do(r.Context(), a.Name)
			if err != nil {
				s.appErr(w, err)
				return
			}
			reply(w, r, 200, a)
		}))
	}

	// ---- variáveis de ambiente ----
	mux.HandleFunc("GET /api/v1/apps/{name}/env", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		list, err := s.Apps.EnvList(r.Context(), a.Name)
		if err != nil {
			s.appErr(w, err)
			return
		}
		writeJSON(w, 200, list)
	}))
	mux.HandleFunc("PUT /api/v1/apps/{name}/env", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		var in struct {
			Set   map[string]apps.EnvValue `json:"set"`
			Unset []string                 `json:"unset"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := s.Apps.EnvApply(r.Context(), a.Name, in.Set, in.Unset); err != nil {
			s.appErr(w, err)
			return
		}
		restarted := false
		if r.URL.Query().Get("restart") == "true" {
			_, err := s.Apps.Restart(r.Context(), a.Name)
			if err != nil && !errors.Is(err, apps.ErrNoRelease) {
				s.appErr(w, err)
				return
			}
			restarted = err == nil
		}
		list, _ := s.Apps.EnvList(r.Context(), a.Name)
		writeJSON(w, 200, map[string]any{"env": list, "restarted": restarted})
	}))
	mux.HandleFunc("DELETE /api/v1/apps/{name}/env/{key}", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		if err := s.Apps.EnvApply(r.Context(), a.Name, nil, []string{r.PathValue("key")}); err != nil {
			s.appErr(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	// revelar um segredo é uma ação explícita e auditada (§8.8)
	mux.HandleFunc("GET /api/v1/apps/{name}/env/{key}", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		v, err := s.Apps.EnvReveal(r.Context(), a.Name, r.PathValue("key"))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fail(w, 404, "env_not_found", "variável inexistente")
				return
			}
			s.appErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"key": r.PathValue("key"), "value": v})
	}))

	// ---- logs de runtime ----
	mux.HandleFunc("GET /api/v1/apps/{name}/logs", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		tail := 200
		if n, err := strconv.Atoi(r.URL.Query().Get("tail")); err == nil && n >= 0 {
			tail = min(n, 5000)
		}
		dir := s.Apps.LogDir(a.Name)
		if r.URL.Query().Get("follow") != "1" {
			ls, err := logs.Tail(dir, tail)
			if err != nil {
				s.internalErr(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			for _, l := range ls {
				io.WriteString(w, l.Text+"\n")
			}
			return
		}
		sse := startSSE(w)
		if sse == nil {
			fail(w, 500, "internal", "streaming não suportado")
			return
		}
		defer sse.close()
		n := 0
		logs.Follow(r.Context(), dir, tail, 500*time.Millisecond, func(l logs.Line) {
			n++
			b, _ := json.Marshal(l)
			sse.event("line", strconv.Itoa(n), string(b))
		})
	}))

	// ---- domínios ----
	mux.HandleFunc("GET /api/v1/apps/{name}/domains", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		ds, err := s.Store.Domains(r.Context(), a.ID)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		writeJSON(w, 200, ds)
	}))
	mux.HandleFunc("POST /api/v1/apps/{name}/domains", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		var in struct {
			Hostname string `json:"hostname"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := s.Apps.AddDomain(r.Context(), a.Name, in.Hostname); err != nil {
			s.appErr(w, err)
			return
		}
		ds, _ := s.Store.Domains(r.Context(), a.ID)
		writeJSON(w, 201, ds)
	}))
	mux.HandleFunc("DELETE /api/v1/apps/{name}/domains/{hostname}", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		if err := s.Apps.RemoveDomain(r.Context(), a.Name, strings.ToLower(r.PathValue("hostname"))); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fail(w, 404, "domain_not_found", "domínio inexistente")
				return
			}
			s.appErr(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
