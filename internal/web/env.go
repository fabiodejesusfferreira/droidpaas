package web

import (
	"net/http"
	"strings"

	"droidpaas/internal/apps"
	"droidpaas/internal/logs"
	"droidpaas/internal/sse"
)

type envData struct {
	AppPage
	Entries []apps.EnvEntry
	Mask    string
	Spare   []int // linhas em branco para novas variáveis
}

func (s *Server) envPage(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	list, err := s.API.Apps.EnvList(c.r.Context(), a.Name)
	if err != nil {
		c.flashErr(err)
	}
	v := c.view(a.Name, "apps")
	v.Data = envData{AppPage: s.appPageBase(c, a, "environment"), Entries: list, Mask: apps.Masked, Spare: []int{0, 1, 2}}
	c.render(http.StatusOK, "app_env", v)
}

// envSave aplica a tabela inteira de uma vez. Segredo cujo valor continua mascarado fica como está.
func (s *Server) envSave(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	r := c.r
	if err := r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	set := map[string]apps.EnvValue{}
	var unset []string
	existing, _ := s.API.Apps.EnvList(r.Context(), a.Name)
	cur := map[string]apps.EnvEntry{}
	for _, e := range existing {
		cur[e.Key] = e
	}
	keys, vals := r.PostForm["k"], r.PostForm["v"]
	for i, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || i >= len(vals) {
			continue
		}
		idx := formIndex(i)
		if r.PostFormValue("d"+idx) != "" { // remover
			unset = append(unset, k)
			continue
		}
		val, secret := vals[i], r.PostFormValue("s"+idx) != ""
		if e, had := cur[k]; had && e.Secret && val == apps.Masked {
			continue // segredo intocado (para trocar o valor ou deixar de ser segredo é preciso digitar o valor)
		}
		if e, had := cur[k]; had && !e.Secret && !secret && e.Value == val {
			continue // sem mudança
		}
		set[k] = apps.EnvValue{Value: val, Secret: secret}
	}
	if len(set) == 0 && len(unset) == 0 {
		c.ok("env.nochange")
		c.redirect("/apps/" + a.Name + "/environment")
		return
	}
	if err := s.API.Apps.EnvApply(r.Context(), a.Name, set, unset); err != nil {
		c.flashErr(err)
		c.redirect("/apps/" + a.Name + "/environment")
		return
	}
	if r.PostFormValue("restart") != "" {
		if _, err := s.API.Apps.Restart(r.Context(), a.Name); err != nil && err != apps.ErrNoRelease {
			c.flashErr(err)
		} else {
			c.ok("env.saved_restarted")
		}
	} else {
		c.ok("env.saved")
	}
	c.redirect("/apps/" + a.Name + "/environment")
}

func formIndex(i int) string { return string(rune('a'+i/26)) + string(rune('a'+i%26)) }

// envReveal devolve o valor de um segredo como fragmento (htmx); a ação é auditada pelo serviço.
func (s *Server) envReveal(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	if err := c.r.ParseForm(); err != nil {
		http.Error(c.w, "requisição inválida", http.StatusBadRequest)
		return
	}
	v, err := s.API.Apps.EnvReveal(c.r.Context(), a.Name, c.r.PostFormValue("key"))
	if err != nil {
		http.Error(c.w, T("err.not_found"), http.StatusNotFound)
		return
	}
	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.fragmentTo("env_value", v)
}

// ---- logs de runtime ----

type logsData struct {
	AppPage
	Tail int
}

func (s *Server) logsPage(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	var initial string
	if ls, err := logs.Tail(s.API.Apps.LogDir(a.Name), 200); err == nil {
		var b strings.Builder
		for _, l := range ls {
			b.WriteString(SanitizeLine(l.Text) + "\n")
		}
		initial = b.String()
	}
	v := c.view(a.Name, "apps")
	v.Data = struct {
		logsData
		Initial string
	}{logsData{AppPage: s.appPageBase(c, a, "logs"), Tail: 200}, initial}
	c.render(http.StatusOK, "app_logs", v)
}

func (s *Server) logsStream(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	stream := sse.Start(c.w)
	if stream == nil {
		c.fail(http.StatusInternalServerError, "err.generic")
		return
	}
	defer stream.Close()
	n := 0
	logs.Follow(c.r.Context(), s.API.Apps.LogDir(a.Name), 200, 500*1e6, func(l logs.Line) {
		n++
		ts := ""
		if !l.TS.IsZero() {
			ts = l.TS.Local().Format("15:04:05")
		}
		stream.Event("line", itoa(n), jsonLine(ts, SanitizeLine(l.Text)))
	})
}

func (s *Server) logsDownload(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	ls, err := logs.Tail(s.API.Apps.LogDir(a.Name), 5000)
	if err != nil {
		c.fail(http.StatusInternalServerError, "err.generic")
		return
	}
	c.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.w.Header().Set("Content-Disposition", `attachment; filename="`+a.Name+`-logs.txt"`)
	for _, l := range ls {
		c.w.Write([]byte(SanitizeLine(l.Text) + "\n"))
	}
}
