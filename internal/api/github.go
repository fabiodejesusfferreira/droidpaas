package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"droidpaas/internal/ghsrc"
	"droidpaas/internal/store"
)

// GitHub devolve o serviço de integração (criado na primeira vez que alguém usa).
func (s *Server) GitHub() *ghsrc.Service {
	s.ghOnce.Do(func() {
		if s.GH == nil {
			s.GH = &ghsrc.Service{Store: s.Store, Box: s.Box, Deploy: s.Deploy, Cfg: s.Cfg, Events: s.Events, Log: s.Log}
		}
	})
	return s.GH
}

// WebhookURL é o endereço que o GitHub deve chamar: o hostname público do painel, se configurado, ou o host desta requisição.
func (s *Server) WebhookURL(r *http.Request, app string) string {
	if set, err := s.EffectiveSettings(r.Context()); err == nil && set["panel_hostname"] != "" {
		return "https://" + set["panel_hostname"] + "/hooks/github/" + app
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/hooks/github/" + app
}

type ghView struct {
	Connected  bool   `json:"connected"`
	WebhookURL string `json:"webhook_url"`
	Secret     string `json:"secret,omitempty"`
	ghsrc.Info
}

func (s *Server) ghErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ghsrc.ErrURL), errors.Is(err, ghsrc.ErrBranch):
		fail(w, 400, "bad_request", err.Error())
	case errors.Is(err, ghsrc.ErrFetch):
		fail(w, 502, "github_fetch_failed", err.Error())
	case errors.Is(err, ghsrc.ErrNone):
		fail(w, 404, "github_not_connected", err.Error())
	default:
		s.appErr(w, err)
	}
}

func (s *Server) registerGitHub(mux *http.ServeMux) {
	// Público de propósito: quem autentica é a assinatura HMAC (o GitHub não tem como enviar um token de API).
	mux.HandleFunc("POST /hooks/github/{app}", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "corpo grande demais", http.StatusRequestEntityTooLarge)
			return
		}
		code, msg := s.GitHub().Webhook(r.Context(), r.PathValue("app"), r.Header.Get("X-GitHub-Event"), r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-Hub-Signature-256"), body)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(code)
		fmt.Fprintln(w, msg)
	})

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
	view := func(r *http.Request, a store.App, in ghsrc.Info, secret string) ghView {
		return ghView{Connected: true, WebhookURL: s.WebhookURL(r, a.Name), Secret: secret, Info: in}
	}
	mux.HandleFunc("GET /api/v1/apps/{name}/github", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		in, err := s.GitHub().Get(r.Context(), a)
		if errors.Is(err, ghsrc.ErrNone) {
			writeJSON(w, 200, ghView{WebhookURL: s.WebhookURL(r, a.Name)})
			return
		}
		if err != nil {
			s.ghErr(w, err)
			return
		}
		secret := ""
		if r.URL.Query().Get("reveal") == "1" {
			secret, _ = s.GitHub().Secret(r.Context(), a)
		}
		writeJSON(w, 200, view(r, a, in, secret))
	}))
	mux.HandleFunc("PUT /api/v1/apps/{name}/github", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		var in struct {
			URL    string `json:"url"`
			Branch string `json:"branch"`
		}
		if !decode(w, r, &in) {
			return
		}
		info, err := s.GitHub().Connect(r.Context(), a, in.URL, in.Branch)
		if err != nil {
			s.ghErr(w, err)
			return
		}
		secret, _ := s.GitHub().Secret(r.Context(), a)
		writeJSON(w, 200, view(r, a, info, secret))
	}))
	mux.HandleFunc("DELETE /api/v1/apps/{name}/github", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		if err := s.GitHub().Disconnect(r.Context(), a); err != nil {
			s.ghErr(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/v1/apps/{name}/github/secret", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		secret, err := s.GitHub().RotateSecret(r.Context(), a)
		if err != nil {
			s.ghErr(w, err)
			return
		}
		in, _ := s.GitHub().Get(r.Context(), a)
		writeJSON(w, 200, view(r, a, in, secret))
	}))
	mux.HandleFunc("POST /api/v1/apps/{name}/github/sync", one(func(w http.ResponseWriter, r *http.Request, a store.App) {
		d, err := s.GitHub().Sync(r.Context(), a)
		if err != nil {
			s.ghErr(w, err)
			return
		}
		writeJSON(w, 202, d)
	}))
}
