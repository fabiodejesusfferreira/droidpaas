package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"droidpaas/internal/api/apitest"
	"droidpaas/internal/auth"
	"droidpaas/internal/stats"
	"droidpaas/internal/system"
	"droidpaas/internal/web"
)

const adminPass = "senha-de-teste-123"

type panel struct {
	t     *testing.T
	env   *apitest.Env
	web   *web.Server
	ts    *httptest.Server
	probe *system.FakeProbe
	col   *stats.Collector
	now   atomic.Int64 // epoch ms; 0 = relógio real
}

func newPanel(t *testing.T) *panel {
	t.Helper()
	e := apitest.New(t)
	ctx := context.Background()
	h, err := auth.HashPassword(adminPass)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Srv.Store.SetPassword(ctx, 1, h, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	probe := &system.FakeProbe{Bat: system.BatteryInfo{Percent: 77, TempC: 33.4, Charging: true}, Mem: system.MemInfo{TotalKB: 3800000, AvailableKB: 1500000},
		Dsk: system.DiskInfo{TotalBytes: 100 << 30, FreeBytes: 40 << 30}, Procs: map[int]system.ProcStats{}}
	col := stats.New(e.Srv.Store, e.Runit, probe, e.Srv.Cfg)
	e.Srv.Stats = col
	col.Sample(ctx)
	w, err := web.New(e.Srv, col, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	w.Sleep = func(time.Duration) {}
	p := &panel{t: t, env: e, web: w, probe: probe, col: col}
	w.Now = func() time.Time {
		if ms := p.now.Load(); ms != 0 {
			return time.UnixMilli(ms)
		}
		return time.Now()
	}
	mux := http.NewServeMux()
	e.Srv.Routes(mux)
	w.Routes(mux)
	p.ts = httptest.NewServer(mux)
	t.Cleanup(p.ts.Close)
	return p
}

// client é um navegador mínimo: guarda cookies e não segue redirecionamentos (os testes inspecionam o 303).
type client struct {
	p    *panel
	c    *http.Client
	csrf string
}

func (p *panel) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{p: p, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type resp struct {
	*http.Response
	Body string
}

func (r resp) has(t *testing.T, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(r.Body, s) {
			t.Errorf("a resposta (%d) não contém %q:\n%.600s", r.StatusCode, s, r.Body)
		}
	}
}

func (r resp) lacks(t *testing.T, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(r.Body, s) {
			t.Errorf("a resposta (%d) NÃO deveria conter %q:\n%.600s", r.StatusCode, s, r.Body)
		}
	}
}

func (r resp) loc() string { return r.Header.Get("Location") }

func (c *client) do(req *http.Request) resp {
	c.p.t.Helper()
	rs, err := c.c.Do(req)
	if err != nil {
		c.p.t.Fatal(err)
	}
	defer rs.Body.Close()
	b, _ := io.ReadAll(rs.Body)
	return resp{rs, string(b)}
}

func (c *client) get(path string, hdr ...string) resp {
	req, _ := http.NewRequest("GET", c.p.ts.URL+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return c.do(req)
}

func (c *client) post(path string, form url.Values, hdr ...string) resp {
	req, _ := http.NewRequest("POST", c.p.ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return c.do(req)
}

// postCSRF envia o formulário com o token CSRF da sessão (lido da primeira página autenticada).
func (c *client) postCSRF(path string, form url.Values, hdr ...string) resp {
	c.p.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("_csrf", c.token())
	return c.post(path, form, hdr...)
}

var csrfRe = regexp.MustCompile(`X-CSRF-Token":"([^"]+)"`)

func (c *client) token() string {
	if c.csrf == "" {
		m := csrfRe.FindStringSubmatch(c.get("/").Body)
		if m == nil {
			c.p.t.Fatal("sem token CSRF na página (sessão válida?)")
		}
		c.csrf = m[1]
	}
	return c.csrf
}

var loginTokRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// login percorre GET /login + POST /login como um navegador.
func (c *client) login(user, pass string, extra ...string) resp {
	c.p.t.Helper()
	pg := c.get("/login" + strings.Join(extra, ""))
	m := loginTokRe.FindStringSubmatch(pg.Body)
	if m == nil {
		c.p.t.Fatalf("login sem token:\n%s", pg.Body)
	}
	next := ""
	if nm := regexp.MustCompile(`name="next" value="([^"]*)"`).FindStringSubmatch(pg.Body); nm != nil {
		next = nm[1]
	}
	return c.post("/login", url.Values{"_csrf": {m[1]}, "username": {user}, "password": {pass}, "next": {next}})
}

func (p *panel) loggedIn() *client {
	p.t.Helper()
	c := p.client()
	if r := c.login("admin", adminPass); r.StatusCode != 303 {
		p.t.Fatalf("login falhou: %d\n%s", r.StatusCode, r.Body)
	}
	return c
}
