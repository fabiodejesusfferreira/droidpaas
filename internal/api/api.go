// Package api: handlers REST do droidd (TSD §10). Handlers finos: a regra de negócio vive nos pacotes de domínio.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/auth"
	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/deploy"
	"droidpaas/internal/events"
	"droidpaas/internal/ghsrc"
	"droidpaas/internal/gitsrv"
	"droidpaas/internal/runit"
	"droidpaas/internal/stats"
	"droidpaas/internal/store"
)

type Server struct {
	Store   *store.Store
	Events  *events.Log
	Box     *crypt.Box
	Cfg     config.Config
	Version string
	Log     *slog.Logger
	Apps    *apps.Service
	Deploy  *deploy.Engine
	Git     *gitsrv.Service
	Runit   runit.ServiceManager
	Stats   *stats.Collector
	Started time.Time
	GH      *ghsrc.Service // criado sob demanda por GitHub()
	ghOnce  sync.Once
	// OnChange acorda o reconciliador depois de mudar o estado desejado.
	OnChange func()
}

type principal struct {
	UserID, TokenID int64
	Name, Scope     string
}

type ctxKey struct{}

func who(r *http.Request) principal { p, _ := r.Context().Value(ctxKey{}).(principal); return p }

// Public monta um mux só com a API (usado nos testes); o droidd compõe API + painel em um mux único.
func (s *Server) Public() http.Handler {
	mux := http.NewServeMux()
	s.Routes(mux)
	return mux
}

// Routes registra as rotas da API REST, do Git HTTP e do /healthz.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "version": s.Version})
	})
	s.registerTokens(mux)
	s.registerSettings(mux)
	s.registerEvents(mux)
	s.registerApps(mux)
	s.registerDeploys(mux)
	s.registerSSHKeys(mux)
	s.registerGitHTTP(mux)
	s.registerGitHub(mux)
	mux.HandleFunc("GET /api/v1/backup", s.full(s.handleBackup))
	mux.HandleFunc("GET /api/v1/me", s.full(s.handleMe))
	mux.HandleFunc("GET /api/v1/system", s.full(s.handleSystem))
	mux.HandleFunc("GET /api/v1/apps/{name}/stats", s.full(s.handleAppStats))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, 404, "not_found", "rota inexistente")
	})
}

// Internal é o mux do socket Unix 0600 (hook do git e CLI local).
func (s *Server) Internal() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/ping", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("pong\n")) })
	mux.HandleFunc("POST /internal/hook/post-receive", s.handleHook)
	return mux
}

func (s *Server) wake() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// ---- autenticação ----

func (s *Server) authenticate(r *http.Request) (principal, error) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || !strings.HasPrefix(tok, "dp_") {
		return principal{}, errors.New("token ausente")
	}
	t, err := s.Store.TokenByHash(r.Context(), auth.HashToken(tok))
	if err != nil {
		return principal{}, errors.New("token inválido")
	}
	// grava last_used_at no máximo 1x/min por token, para não escrever a cada requisição
	if t.LastUsedAt == nil || time.Since(time.UnixMilli(*t.LastUsedAt)) > time.Minute {
		s.Store.TouchToken(r.Context(), t.ID)
	}
	return principal{UserID: t.UserID, TokenID: t.ID, Name: t.Name, Scope: t.Scope}, nil
}

func (s *Server) guard(needFull bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, 401, "unauthorized", err.Error())
			return
		}
		if needFull && p.Scope != "full" {
			fail(w, 403, "forbidden", "token sem escopo suficiente")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	}
}

func (s *Server) full(h http.HandlerFunc) http.HandlerFunc { return s.guard(true, h) }

// ---- JSON ----

type errBody struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func fail(w http.ResponseWriter, status int, code, msg string) { failD(w, status, code, msg, nil) }

func failD(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	var b errBody
	b.Error.Code, b.Error.Message = code, msg
	b.Error.Details = details
	if details == nil {
		b.Error.Details = map[string]any{}
	}
	writeJSON(w, status, b)
}

// decode lê um corpo JSON estrito (campos desconhecidos viram 400) de até 1 MiB.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		fail(w, 400, "bad_request", "JSON inválido: "+err.Error())
		return false
	}
	return true
}

// page lê ?limit= (padrão 50, máx. 200) e ?before=<id>.
func page(r *http.Request) (limit int, before int64) {
	limit = 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 200)
	}
	before, _ = strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	return
}

func pathID(r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return n, err == nil && n > 0
}

func (s *Server) internalErr(w http.ResponseWriter, err error) {
	s.Log.Error("erro interno", "err", err)
	fail(w, 500, "internal", "erro interno")
}

// ---- handlers ----

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	name, err := s.Store.User(r.Context(), p.UserID)
	if err != nil {
		s.internalErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"username": name, "user_id": p.UserID, "token": map[string]any{"id": p.TokenID, "name": p.Name, "scope": p.Scope}})
}

func (s *Server) registerTokens(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/tokens", s.full(func(w http.ResponseWriter, r *http.Request) {
		ts, err := s.Store.ListTokens(r.Context(), who(r).UserID)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		writeJSON(w, 200, ts)
	}))
	mux.HandleFunc("POST /api/v1/tokens", s.full(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, Scope string }
		if !decode(w, r, &in) {
			return
		}
		id, tok, err := s.CreateToken(r.Context(), who(r).UserID, in.Name, in.Scope)
		if err != nil {
			s.appErr(w, err)
			return
		}
		scope := in.Scope
		if scope == "" {
			scope = "full"
		}
		writeJSON(w, 201, map[string]any{"id": id, "name": strings.TrimSpace(in.Name), "scope": scope, "prefix": tok[:9], "token": tok})
	}))
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.full(func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r)
		if !ok {
			fail(w, 400, "bad_request", "id inválido")
			return
		}
		if err := s.RevokeToken(r.Context(), who(r).UserID, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fail(w, 404, "token_not_found", "token inexistente ou já revogado")
				return
			}
			s.internalErr(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}

func (s *Server) registerEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/events", s.full(func(w http.ResponseWriter, r *http.Request) {
		limit, before := page(r)
		ev, err := s.Events.List(r.Context(), limit, before)
		if err != nil {
			s.internalErr(w, err)
			return
		}
		writeJSON(w, 200, ev)
	}))
}
