package web

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"droidpaas/internal/ghsrc"
	"droidpaas/internal/store"
)

type githubData struct {
	AppPage
	Connected bool
	Info      ghsrc.Info
	Webhook   string
	Public    bool // o painel tem hostname público: o webhook precisa de bypass na Cloudflare Access
	LastSync  string
}

func (s *Server) githubPage(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	d := githubData{AppPage: s.appPageBase(c, a, "github"), Webhook: s.API.WebhookURL(c.r, a.Name)}
	if in, err := s.API.GitHub().Get(c.r.Context(), a); err == nil {
		d.Connected, d.Info = true, in
		if in.LastSync != nil {
			d.LastSync = agoSince(s.Now(), time.UnixMilli(*in.LastSync))
		}
	}
	if set, err := s.API.EffectiveSettings(c.r.Context()); err == nil {
		d.Public = set["panel_hostname"] != ""
	}
	v := c.view(a.Name, "apps")
	v.Data = d
	c.render(http.StatusOK, "app_github", v)
}

func (c *ctx) githubErr(err error) {
	switch {
	case errors.Is(err, ghsrc.ErrURL):
		c.err("github.err.url")
	case errors.Is(err, ghsrc.ErrBranch):
		c.err("github.err.branch")
	case errors.Is(err, ghsrc.ErrNone):
		c.err("github.err.none")
	case errors.Is(err, ghsrc.ErrFetch):
		c.err("github.err.fetch", err.Error())
	default:
		c.flashErr(err)
	}
}

// githubAct roda uma ação do formulário sobre o app e volta para a aba.
func (s *Server) githubAct(c *ctx, f func(a store.App) (okKey string, err error)) {
	a, ok := c.app()
	if !ok {
		return
	}
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	key, err := f(a)
	if err != nil {
		c.githubErr(err)
	} else if key != "" {
		c.ok(key)
	}
	c.redirect("/apps/" + a.Name + "/github")
}

func (s *Server) githubConnect(c *ctx) {
	s.githubAct(c, func(a store.App) (string, error) {
		_, err := s.API.GitHub().Connect(c.r.Context(), a, c.r.PostFormValue("url"), c.r.PostFormValue("branch"))
		return "github.connected", err
	})
}

func (s *Server) githubDisconnect(c *ctx) {
	s.githubAct(c, func(a store.App) (string, error) {
		return "github.disconnected", s.API.GitHub().Disconnect(c.r.Context(), a)
	})
}

func (s *Server) githubSecret(c *ctx) {
	s.githubAct(c, func(a store.App) (string, error) {
		_, err := s.API.GitHub().RotateSecret(c.r.Context(), a)
		return "github.secret.rotated", err
	})
}

func (s *Server) githubSync(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	d, err := s.API.GitHub().Sync(c.r.Context(), a)
	if err != nil {
		c.githubErr(err)
		c.redirect("/apps/" + a.Name + "/github")
		return
	}
	c.redirect(fmt.Sprintf("/apps/%s/deployments/%d", a.Name, d.ID))
}

func (s *Server) githubReveal(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	secret, err := s.API.GitHub().Secret(c.r.Context(), a)
	if err != nil {
		http.Error(c.w, T("err.not_found"), http.StatusNotFound)
		return
	}
	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.fragmentTo("env_value", secret)
}
