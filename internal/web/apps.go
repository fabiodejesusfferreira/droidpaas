package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"droidpaas/internal/apps"
	"droidpaas/internal/store"
)

// flashErr traduz um erro de domínio para uma mensagem amigável do painel.
func (c *ctx) flashErr(err error) {
	var ve *apps.ValidationError
	switch {
	case errors.As(err, &ve):
		c.err("err.validation", ve.Field, ve.Msg)
	case errors.Is(err, apps.ErrConflict), errors.Is(err, store.ErrExists):
		c.err("err.conflict")
	case errors.Is(err, apps.ErrNoRelease):
		c.err("err.no_release")
	case errors.Is(err, store.ErrNotFound):
		c.err("err.not_found")
	default:
		c.s.Log.Error("ação do painel", "err", err)
		c.err("err.generic")
	}
}

// ---- assistente de criação (RF-32) ----

type wizData struct {
	Step                           int
	Name, Branch, Runtime          string
	BuildCmd, StartCmd, HealthPath string
	Err                            string
	Runtimes                       []string
	HasKeys                        bool
	BaseDomain                     string
}

var runtimeChoices = []string{"auto", "node", "python", "go", "static", "custom"}

func (s *Server) wizData(c *ctx, step int) wizData {
	keys, _ := s.API.Store.SSHKeys(c.r.Context())
	return wizData{Step: step, Branch: "main", Runtime: "auto", Runtimes: runtimeChoices, HasKeys: len(keys) > 0,
		BaseDomain: s.API.Apps.BaseDomain(c.r.Context())}
}

func (s *Server) wizard(c *ctx) {
	v := c.view(T("wizard.title"), "new")
	v.Data = s.wizData(c, 1)
	c.render(http.StatusOK, "app_new", v)
}

func (s *Server) wizardSubmit(c *ctx) {
	r := c.r
	if err := r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	d := s.wizData(c, 1)
	step, _ := strconv.Atoi(r.PostFormValue("step"))
	d.Name = strings.TrimSpace(r.PostFormValue("name"))
	d.Branch = strings.TrimSpace(r.PostFormValue("branch"))
	if f := r.PostFormValue("runtime"); f != "" {
		d.Runtime = f
	}
	d.BuildCmd, d.StartCmd, d.HealthPath = strings.TrimSpace(r.PostFormValue("build_cmd")), strings.TrimSpace(r.PostFormValue("start_cmd")), strings.TrimSpace(r.PostFormValue("health_path"))
	if r.PostFormValue("back") != "" { // botão Voltar
		d.Step = max(step-1, 1)
		s.renderWizard(c, d, 200)
		return
	}
	// valida os passos já concluídos antes de avançar (a mesma regra do Create)
	d.Step = step + 1
	if step == 1 && !validName(d.Name) {
		d.Step, d.Err = 1, "wizard.err.name"
	} else if step == 2 && !validRuntime(d.Runtime) {
		d.Step, d.Err = 2, "wizard.err.runtime"
	}
	if d.Err == "" && step < 3 {
		s.renderWizard(c, d, 200)
		return
	}
	if d.Err != "" {
		s.renderWizard(c, d, http.StatusUnprocessableEntity)
		return
	}
	// passo 3 confirmado: cria o app e aplica os campos opcionais
	ctx := r.Context()
	if _, err := s.API.Apps.Create(ctx, apps.CreateInput{Name: d.Name, Runtime: d.Runtime, Branch: d.Branch}); err != nil {
		var ve *apps.ValidationError
		switch {
		case errors.As(err, &ve):
			d.Err = ve.Field + ": " + ve.Msg
		case errors.Is(err, apps.ErrConflict):
			d.Err = T("err.conflict")
		default:
			s.Log.Error("criar app", "err", err)
			d.Err = T("err.generic")
		}
		d.Step = 1
		s.renderWizard(c, d, http.StatusUnprocessableEntity)
		return
	}
	patch := apps.Patch{BuildCmd: &d.BuildCmd, StartCmd: &d.StartCmd, HealthPath: &d.HealthPath}
	if _, err := s.API.Apps.Update(ctx, d.Name, patch); err != nil {
		c.flashErr(err) // o app existe; os campos opcionais podem ser corrigidos em Configurações
	}
	c.ok("wizard.created", d.Name)
	c.redirect("/apps/" + d.Name + "/publish")
}

func (s *Server) renderWizard(c *ctx, d wizData, code int) {
	v := c.view(T("wizard.title"), "new")
	v.Data = d
	c.render(code, "app_new", v)
}

func validName(n string) bool {
	return len(n) >= 2 && len(n) <= 31 && n[0] >= 'a' && n[0] <= 'z' && strings.Trim(n, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

func validRuntime(r string) bool {
	for _, x := range runtimeChoices {
		if x == r {
			return true
		}
	}
	return false
}

type publishData struct {
	App             store.App
	SSHURL, HTTPURL string
	HasKeys         bool
	Domain          string
}

func (s *Server) appPublish(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	ssh, httpURL := s.API.GitURLs(c.r, a.Name)
	keys, _ := s.API.Store.SSHKeys(c.r.Context())
	ds, _ := s.API.Store.Domains(c.r.Context(), a.ID)
	dom := ""
	if len(ds) > 0 {
		dom = ds[0].Hostname
	}
	v := c.view(a.Name, "apps")
	v.Data = publishData{App: a, SSHURL: ssh, HTTPURL: httpURL, HasKeys: len(keys) > 0, Domain: dom}
	c.render(http.StatusOK, "app_publish", v)
}

// ---- visão geral (RF-33) ----

type AppPage struct {
	App   store.App
	Card  appCard
	Tab   string
	Extra any
}

type deployRow struct {
	D        store.Deploy
	Duration string
	SHA      string
	Current  bool
}

func (s *Server) deployRows(c *ctx, a store.App, limit int, before int64) ([]deployRow, int64, error) {
	ds, err := s.API.Store.Deploys(c.r.Context(), a.ID, limit+1, before)
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(ds) > limit {
		ds, next = ds[:limit], ds[limit-1].ID
	}
	rows := make([]deployRow, len(ds))
	for i, d := range ds {
		rows[i] = deployRow{D: d, SHA: shortSHA(d.GitSHA)}
		if d.StartedAt != nil && d.FinishedAt != nil {
			rows[i].Duration = fmtDuration(*d.FinishedAt - *d.StartedAt)
		}
		if a.CurrentReleaseID != nil && d.ReleaseID != nil && *d.ReleaseID == *a.CurrentReleaseID && d.Status == "succeeded" && i == firstSucceeded(ds) {
			rows[i].Current = true
		}
	}
	return rows, next, nil
}

func firstSucceeded(ds []store.Deploy) int {
	for i, d := range ds {
		if d.Status == "succeeded" {
			return i
		}
	}
	return -1
}

type overviewData struct {
	AppPage
	SSHURL, HTTPURL string
	Deploys         []deployRow
}

func (s *Server) appPageBase(c *ctx, a store.App, tab string) AppPage {
	scheme := s.publicScheme(c)
	ds, _ := s.API.Store.Domains(c.r.Context(), a.ID)
	var names []string
	for _, d := range ds {
		names = append(names, d.Hostname)
	}
	return AppPage{App: a, Card: s.card(c, a, names, scheme), Tab: tab}
}

func (s *Server) appOverview(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	rows, _, _ := s.deployRows(c, a, 5, 0)
	ssh, httpURL := s.API.GitURLs(c.r, a.Name)
	v := c.view(a.Name, "apps")
	v.Data = overviewData{AppPage: s.appPageBase(c, a, "overview"), SSHURL: ssh, HTTPURL: httpURL, Deploys: rows}
	c.render(http.StatusOK, "app_overview", v)
}

func (s *Server) fragAppStats(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	c.fragment("app_stats", s.card(c, a, nil, "http"))
}

func (s *Server) appAction(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	var err error
	switch act := c.r.PathValue("action"); act {
	case "start":
		_, err = s.API.Apps.Start(c.r.Context(), a.Name)
	case "stop":
		_, err = s.API.Apps.Stop(c.r.Context(), a.Name)
	case "restart":
		_, err = s.API.Apps.Restart(c.r.Context(), a.Name)
	default:
		c.fail(http.StatusNotFound, "err.notfound")
		return
	}
	if err != nil {
		c.flashErr(err)
	} else {
		c.ok("action." + c.r.PathValue("action") + ".ok")
	}
	c.redirect("/apps/" + a.Name)
}

// ---- configurações do app ----

type settingsData struct {
	AppPage
	Runtimes []string
}

func (s *Server) appSettings(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	v := c.view(a.Name, "apps")
	v.Data = settingsData{AppPage: s.appPageBase(c, a, "settings"), Runtimes: runtimeChoices}
	c.render(http.StatusOK, "app_settings", v)
}

func (s *Server) appSettingsSave(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	r := c.r
	if err := r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	str := func(k string) *string { v := strings.TrimSpace(r.PostFormValue(k)); return &v }
	p := apps.Patch{Runtime: str("runtime"), Branch: str("branch"), BuildCmd: str("build_cmd"), StartCmd: str("start_cmd"),
		HealthPath: str("health_path"), StaticDir: str("static_dir")}
	if n, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("health_timeout_s"))); err == nil {
		p.HealthTimeoutS = &n
	} else {
		c.err("err.validation", "health_timeout_s", "número inválido")
		c.redirect("/apps/" + a.Name + "/settings")
		return
	}
	if n, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("nice"))); err == nil {
		p.Nice = &n
	} else {
		c.err("err.validation", "nice", "número inválido")
		c.redirect("/apps/" + a.Name + "/settings")
		return
	}
	if _, err := s.API.Apps.Update(r.Context(), a.Name, p); err != nil {
		c.flashErr(err)
	} else {
		c.ok("settings.saved")
	}
	c.redirect("/apps/" + a.Name + "/settings")
}

// appDelete exige digitar o nome do app (zona de perigo, TSD §12.4).
func (s *Server) appDelete(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	if err := s.API.Apps.Delete(c.r.Context(), a.Name, strings.TrimSpace(c.r.PostFormValue("confirm"))); err != nil {
		c.flashErr(err)
		c.redirect("/apps/" + a.Name + "/settings")
		return
	}
	c.ok("app.deleted", a.Name)
	c.redirect("/")
}

// ---- domínios ----

type domainsData struct {
	AppPage
	Domains    []store.Domain
	BaseDomain string
	Tunnel     string
}

func (s *Server) domainsPage(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	ds, _ := s.API.Store.Domains(c.r.Context(), a.ID)
	v := c.view(a.Name, "apps")
	v.Data = domainsData{AppPage: s.appPageBase(c, a, "domains"), Domains: ds, BaseDomain: s.API.Apps.BaseDomain(c.r.Context()), Tunnel: s.tunnelState(c)}
	c.render(http.StatusOK, "app_domains", v)
}

func (s *Server) domainAdd(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	if err := s.API.Apps.AddDomain(c.r.Context(), a.Name, c.r.PostFormValue("hostname")); err != nil {
		c.flashErr(err)
	} else {
		c.ok("domains.added")
	}
	c.redirect("/apps/" + a.Name + "/domains")
}

func (s *Server) domainRemove(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	if err := s.API.Apps.RemoveDomain(c.r.Context(), a.Name, strings.ToLower(c.r.PostFormValue("hostname"))); err != nil {
		c.flashErr(err)
	} else {
		c.ok("domains.removed")
	}
	c.redirect("/apps/" + a.Name + "/domains")
}
