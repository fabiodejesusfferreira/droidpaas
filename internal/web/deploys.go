package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"droidpaas/internal/deploy"
	"droidpaas/internal/sse"
	"droidpaas/internal/store"
)

type deploymentsData struct {
	AppPage
	Rows     []deployRow
	Next     int64
	Rollback []store.Release // releases ready (menos a atual) elegíveis ao rollback
}

func (s *Server) deployments(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	before, _ := strconv.ParseInt(c.r.URL.Query().Get("before"), 10, 64)
	rows, next, err := s.deployRows(c, a, 20, before)
	if err != nil {
		c.fail(http.StatusInternalServerError, "err.generic")
		return
	}
	rels, _ := s.API.Store.Releases(c.r.Context(), a.ID, "ready")
	var rb []store.Release
	for _, r := range rels {
		if a.CurrentReleaseID == nil || r.ID != *a.CurrentReleaseID {
			rb = append(rb, r)
		}
	}
	v := c.view(a.Name, "apps")
	v.Data = deploymentsData{AppPage: s.appPageBase(c, a, "deployments"), Rows: rows, Next: next, Rollback: rb}
	c.render(http.StatusOK, "app_deployments", v)
}

func (c *ctx) deployErr(err error) {
	switch {
	case errors.Is(err, deploy.ErrBadRef):
		c.err("deploy.err.bad_ref")
	case errors.Is(err, deploy.ErrNoPrevious):
		c.err("deploy.err.no_previous")
	case errors.Is(err, deploy.ErrBadRelease):
		c.err("deploy.err.bad_release")
	case errors.Is(err, deploy.ErrNotActive):
		c.err("deploy.err.not_active")
	case errors.Is(err, deploy.ErrTooLate):
		c.err("deploy.err.too_late")
	default:
		c.flashErr(err)
	}
}

func (s *Server) deployNow(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	trigger := "manual"
	if c.r.PostFormValue("rebuild") != "" {
		trigger = "rebuild"
	}
	d, err := s.API.Deploy.Enqueue(c.r.Context(), a.Name, trigger, deploy.EnqueueOpts{Ref: strings.TrimSpace(c.r.PostFormValue("ref"))})
	if err != nil {
		c.deployErr(err)
		c.redirect("/apps/" + a.Name + "/deployments")
		return
	}
	c.redirect("/apps/" + a.Name + "/deployments/" + strconv.FormatInt(d.ID, 10))
}

func (s *Server) rollbackNow(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	if err := c.r.ParseForm(); err != nil {
		c.fail(http.StatusBadRequest, "err.generic")
		return
	}
	rid, _ := strconv.ParseInt(c.r.PostFormValue("release_id"), 10, 64)
	d, err := s.API.Deploy.Enqueue(c.r.Context(), a.Name, "rollback", deploy.EnqueueOpts{ReleaseID: rid})
	if err != nil {
		c.deployErr(err)
		c.redirect("/apps/" + a.Name + "/deployments")
		return
	}
	c.redirect("/apps/" + a.Name + "/deployments/" + strconv.FormatInt(d.ID, 10))
}

type deploymentData struct {
	AppPage
	D    store.Deploy
	SHA  string
	Dur  string
	Live bool
	Log  string
	Rel  *store.Release
}

// deployOf carrega o deploy garantindo que ele pertence ao app da URL.
func (c *ctx) deployOf(a store.App) (store.Deploy, bool) {
	id, err := strconv.ParseInt(c.r.PathValue("id"), 10, 64)
	if err != nil {
		c.fail(http.StatusNotFound, "err.notfound")
		return store.Deploy{}, false
	}
	d, err := c.s.API.Store.Deploy(c.r.Context(), id)
	if err != nil || d.AppID != a.ID {
		c.fail(http.StatusNotFound, "err.notfound")
		return d, false
	}
	return d, true
}

const maxLogRender = 512 << 10

func (s *Server) deployment(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	d, ok := c.deployOf(a)
	if !ok {
		return
	}
	dd := deploymentData{AppPage: s.appPageBase(c, a, "deployments"), D: d, SHA: shortSHA(d.GitSHA), Live: d.Status == "queued" || d.Status == "running"}
	if d.StartedAt != nil && d.FinishedAt != nil {
		dd.Dur = fmtDuration(*d.FinishedAt - *d.StartedAt)
	}
	if d.ReleaseID != nil {
		if r, err := s.API.Store.Release(c.r.Context(), *d.ReleaseID); err == nil {
			dd.Rel = &r
		}
	}
	if !dd.Live { // deploy terminado: o log vem do arquivo, já limpo de ANSI/controles
		dd.Log = readTail(d.LogPath, maxLogRender)
	}
	v := c.view("#"+strconv.FormatInt(d.ID, 10)+" · "+a.Name, "apps")
	v.Data = dd
	c.render(http.StatusOK, "app_deployment", v)
}

func readTail(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	prefix := ""
	if fi.Size() > max {
		f.Seek(fi.Size()-max, io.SeekStart)
		prefix = "[…início do log omitido…]\n"
	}
	b, _ := io.ReadAll(io.LimitReader(f, max))
	return prefix + SanitizeLog(string(b))
}

// deploymentStream: log do deploy ao vivo por SSE (RF-34). Retoma por Last-Event-ID; cada linha é limpa antes de sair.
func (s *Server) deploymentStream(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	d, ok := c.deployOf(a)
	if !ok {
		return
	}
	from, _ := strconv.Atoi(c.r.Header.Get("Last-Event-ID"))
	stream := sse.Start(c.w)
	if stream == nil {
		c.fail(http.StatusInternalServerError, "err.generic")
		return
	}
	defer stream.Close()
	status, err := s.API.Deploy.Follow(c.r.Context(), d.ID, from, func(n int, line string) {
		b, _ := json.Marshal(map[string]any{"n": n, "text": SanitizeLine(line)})
		stream.Event("line", strconv.Itoa(n), string(b))
	})
	if err != nil {
		return
	}
	b, _ := json.Marshal(map[string]string{"status": status})
	stream.Event("end", "", string(b))
}

func (s *Server) deploymentCancel(c *ctx) {
	a, ok := c.app()
	if !ok {
		return
	}
	d, ok := c.deployOf(a)
	if !ok {
		return
	}
	if err := s.API.Deploy.Cancel(c.r.Context(), d.ID); err != nil {
		c.deployErr(err)
	} else {
		c.ok("deploys.cancel.ok")
	}
	c.redirect("/apps/" + a.Name + "/deployments/" + strconv.FormatInt(d.ID, 10))
}
