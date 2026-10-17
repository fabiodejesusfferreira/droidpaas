package web

import (
	"droidpaas/internal/api"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"droidpaas/internal/auth"
	"droidpaas/internal/store"
)

var settingsTabs = []string{"general", "tunnel", "keys", "tokens", "account"}

type settingsPageData struct {
	Tab        string
	Tabs       []string
	Settings   map[string]string
	Tunnel     string
	TunnelSet  bool
	Keys       []store.SSHKey
	Tokens     []store.Token
	Sessions   int
	BaseDomain string
}

func (s *Server) settingsPage(c *ctx) {
	tab := c.r.PathValue("tab")
	if tab == "" {
		tab = "general"
	}
	ok := false
	for _, t := range settingsTabs {
		ok = ok || t == tab
	}
	if !ok {
		c.fail(http.StatusNotFound, "err.notfound")
		return
	}
	ctx := c.r.Context()
	d := settingsPageData{Tab: tab, Tabs: settingsTabs}
	switch tab {
	case "general":
		d.Settings, _ = s.API.EffectiveSettings(ctx)
	case "tunnel":
		d.Tunnel = s.tunnelState(c)
		_, d.TunnelSet, _ = s.API.Store.Setting(ctx, "tunnel_token")
		d.Settings, _ = s.API.EffectiveSettings(ctx)
	case "keys":
		d.Keys, _ = s.API.Store.SSHKeys(ctx)
	case "tokens":
		d.Tokens, _ = s.API.Store.ListTokens(ctx, c.sess.UserID)
	case "account":
		d.Sessions, _ = s.API.Store.CountSessions(ctx, c.sess.UserID)
	}
	v := c.view(T("nav.settings"), "settings")
	v.Data = d
	c.render(http.StatusOK, "settings", v)
}

func (c *ctx) back(tab string) { c.redirect("/settings/" + tab) }

func (s *Server) settingsGeneral(c *ctx) {
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	in := map[string]string{
		"base_domain":      strings.ToLower(strings.TrimSpace(c.r.PostFormValue("base_domain"))),
		"panel_hostname":   strings.ToLower(strings.TrimSpace(c.r.PostFormValue("panel_hostname"))),
		"keep_releases":    strings.TrimSpace(c.r.PostFormValue("keep_releases")),
		"git_http_enabled": strconv.FormatBool(c.r.PostFormValue("git_http_enabled") != ""),
	}
	if err := s.API.ApplySettings(c.r.Context(), in); err != nil {
		c.flashErr(err)
	} else {
		c.ok("settings.saved")
	}
	c.back("general")
}

func (s *Server) settingsTunnel(c *ctx) {
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	token := ""
	if c.r.PostFormValue("clear") == "" {
		token = c.r.PostFormValue("token")
		if strings.TrimSpace(token) == "" {
			c.err("tunnel.err.empty")
			c.back("tunnel")
			return
		}
	}
	if err := s.API.SetTunnelToken(c.r.Context(), token); err != nil {
		c.flashErr(err)
	} else if token == "" {
		c.ok("tunnel.cleared")
	} else {
		c.ok("tunnel.saved")
	}
	c.back("tunnel")
}

func (s *Server) keyAdd(c *ctx) {
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	k, err := s.API.AddSSHKey(c.r.Context(), c.sess.UserID, c.r.PostFormValue("name"), c.r.PostFormValue("public_key"))
	switch {
	case err == nil:
		c.ok("keys.added", k.Fingerprint)
	case errors.Is(err, api.ErrKeyExists):
		c.err("keys.exists")
	default:
		c.flashErr(err)
	}
	c.back("keys")
}

func (s *Server) keyRemove(c *ctx) {
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	id, _ := strconv.ParseInt(c.r.PostFormValue("id"), 10, 64)
	if err := s.API.RemoveSSHKey(c.r.Context(), id); err != nil {
		c.flashErr(err)
	} else {
		c.ok("keys.removed")
	}
	c.back("keys")
}

func (s *Server) tokenCreate(c *ctx) {
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	_, tok, err := s.API.CreateToken(c.r.Context(), c.sess.UserID, c.r.PostFormValue("name"), c.r.PostFormValue("scope"))
	if err != nil {
		c.flashErr(err)
	} else {
		s.showOnce(c.sess.ID, tok) // exibido uma única vez, na próxima página
		c.ok("tokens.created")
	}
	c.back("tokens")
}

func (s *Server) tokenRevoke(c *ctx) {
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	id, _ := strconv.ParseInt(c.r.PostFormValue("id"), 10, 64)
	if err := s.API.RevokeToken(c.r.Context(), c.sess.UserID, id); err != nil {
		c.flashErr(err)
	} else {
		c.ok("tokens.revoked.ok")
	}
	c.back("tokens")
}

// accountPassword troca a senha (RF-41): confere a atual, exige 12+ caracteres e encerra as outras sessões.
func (s *Server) accountPassword(c *ctx) {
	r := c.r
	if err := r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	key, now := "pw:"+strconv.FormatInt(c.sess.UserID, 10), s.Now()
	if b, _ := s.lim.blocked(key, now); b {
		c.err("login.toomany")
		c.back("account")
		return
	}
	hash, err := s.API.Store.UserHash(r.Context(), c.sess.UserID)
	if err != nil || !auth.VerifyPassword(r.PostFormValue("current"), hash) {
		s.lim.fail(key, now)
		s.API.Events.Warn(r.Context(), "auth.password_change_failed", nil, "senha atual incorreta ao trocar a senha", nil)
		c.err("account.err.current")
		c.back("account")
		return
	}
	pw := r.PostFormValue("new")
	if pw != r.PostFormValue("confirm") {
		c.err("account.err.mismatch")
		c.back("account")
		return
	}
	nh, err := auth.HashPassword(pw)
	if err != nil {
		c.err("account.err.short", auth.MinPasswordLen)
		c.back("account")
		return
	}
	if err := s.API.Store.SetPassword(r.Context(), c.sess.UserID, nh, now.UnixMilli()); err != nil {
		c.flashErr(err)
		c.back("account")
		return
	}
	s.lim.reset(key)
	n, _ := s.API.Store.DeleteOtherSessions(r.Context(), c.sess.UserID, c.sess.ID)
	s.API.Events.Info(r.Context(), "auth.password_changed", nil, "senha do administrador alterada", map[string]any{"sessoes_encerradas": n})
	c.ok("account.changed", n)
	c.back("account")
}

// ---- eventos (RF-38) ----

type eventsData struct {
	Events []eventRow
	Next   int64
}

type eventRow struct {
	ID      int64
	TS      time.Time
	Level   string
	Kind    string
	Message string
	App     string
	Data    string
}

func (s *Server) eventsPage(c *ctx) {
	before, _ := strconv.ParseInt(c.r.URL.Query().Get("before"), 10, 64)
	const page = 50
	evs, err := s.API.Events.List(c.r.Context(), page+1, before)
	if err != nil {
		c.fail(http.StatusInternalServerError, "err.generic")
		return
	}
	var next int64
	if len(evs) > page {
		evs, next = evs[:page], evs[page-1].ID
	}
	names := map[int64]string{}
	if list, err := s.API.Apps.List(c.r.Context()); err == nil {
		for _, a := range list {
			names[a.ID] = a.Name
		}
	}
	rows := make([]eventRow, len(evs))
	for i, e := range evs {
		rows[i] = eventRow{ID: e.ID, TS: e.TS, Level: e.Level, Kind: e.Kind, Message: SanitizeLine(e.Message), Data: SanitizeLog(string(e.Data))}
		if e.AppID != nil {
			rows[i].App = names[*e.AppID]
		}
	}
	v := c.view(T("nav.events"), "events")
	v.Data = eventsData{Events: rows, Next: next}
	c.render(http.StatusOK, "events", v)
}
