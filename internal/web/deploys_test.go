package web_test

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"droidpaas/internal/store"
	"droidpaas/internal/system"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		if _, err2 := os.Stat("/usr/local/bin/git"); err2 != nil {
			t.Skip("git indisponível")
		}
	}
}

func (p *panel) waitDeploy(id int64) store.Deploy {
	p.t.Helper()
	for i := 0; i < 600; i++ {
		d, err := p.env.Srv.Store.Deploy(p.t.Context(), id)
		if err == nil && d.Status != "queued" && d.Status != "running" {
			return d
		}
		time.Sleep(25 * time.Millisecond)
	}
	p.t.Fatalf("deploy %d não terminou", id)
	return store.Deploy{}
}

// sse lê um stream SSE até o evento `end` (ou o limite) e devolve os pares evento/dados.
func (c *client) sse(path string, hdr ...string) (events [][2]string) {
	c.p.t.Helper()
	req, _ := http.NewRequest("GET", c.p.ts.URL+path, nil)
	req.Header.Set("Accept", "text/event-stream")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rs, err := c.c.Do(req)
	if err != nil {
		c.p.t.Fatal(err)
	}
	defer rs.Body.Close()
	if ct := rs.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		c.p.t.Fatalf("Content-Type %q (%d)", ct, rs.StatusCode)
	}
	sc := bufio.NewScanner(rs.Body)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	var ev string
	deadline := time.Now().Add(15 * time.Second)
	for sc.Scan() && time.Now().Before(deadline) {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "event: "):
			ev = l[7:]
		case strings.HasPrefix(l, "data: "):
			events = append(events, [2]string{ev, l[6:]})
			if ev == "end" {
				return
			}
		}
	}
	return
}

func TestDeployThroughThePanel(t *testing.T) { // AT-23, AT-26
	needGit(t)
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	// sem commits: erro amigável
	c.postCSRF("/apps/blog/deployments", nil)
	c.get("/apps/blog/deployments").has(t, "não existe no repositório")
	// v1
	p.env.PushSite(t, "blog", map[string]string{"index.html": "v1"})
	r := c.postCSRF("/apps/blog/deployments", nil)
	if r.StatusCode != 303 || r.loc() != "/apps/blog/deployments/1" {
		t.Fatalf("%d %q", r.StatusCode, r.loc())
	}
	if d := p.waitDeploy(1); d.Status != "succeeded" {
		t.Fatalf("%s %v", d.Status, d.Error)
	}
	pg := c.get(r.loc())
	pg.has(t, "Deploy #1", "concluído", "-----&gt; fetch", "runtime: static", "-----&gt; finalize", "manual")
	pg.lacks(t, "data-log") // terminado: log estático, sem EventSource
	// o histórico lista, marca a release atual e a visão geral mostra o commit
	c.get("/apps/blog/deployments").has(t, "#1", "concluído", "atual")
	c.get("/apps/blog").has(t, "Release atual", "Últimos deploys", "static")
	// v2 → rollback para a v1 pelo painel (AT-26)
	time.Sleep(1100 * time.Millisecond) // nomes de release usam segundos
	p.env.PushSite(t, "blog", map[string]string{"index.html": "v2"})
	c.postCSRF("/apps/blog/deployments", form("rebuild", "1"))
	if d := p.waitDeploy(2); d.Status != "succeeded" || !d.Rebuild || d.Trigger != "rebuild" {
		t.Fatalf("%+v", d)
	}
	pg = c.get("/apps/blog/deployments")
	pg.has(t, "Voltar para", "Rollback")
	rid := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(pg.Body)
	if rid == nil {
		t.Fatalf("sem release para rollback:\n%s", pg.Body)
	}
	r = c.postCSRF("/apps/blog/rollback", form("release_id", rid[1]))
	if r.StatusCode != 303 || r.loc() != "/apps/blog/deployments/3" {
		t.Fatalf("%d %q", r.StatusCode, r.loc())
	}
	if d := p.waitDeploy(3); d.Status != "succeeded" || d.Trigger != "rollback" {
		t.Fatalf("%+v", d)
	}
	c.get("/apps/blog/deployments/3").has(t, "rollback", "concluído")
	// deploy de outro app não aparece na URL errada
	c.createApp("outro")
	if r := c.get("/apps/outro/deployments/1"); r.StatusCode != 404 {
		t.Fatalf("deploy de outro app: %d", r.StatusCode)
	}
	if r := c.get("/apps/blog/deployments/9999"); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
}

func TestLiveDeployLogAndCancel(t *testing.T) { // AT-23
	needGit(t)
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	p.env.PushSite(t, "blog", map[string]string{"index.html": "x", "droidpaas.toml": "runtime = \"static\"\nbuild = \"echo comecou; sleep 30\"\n"})
	r := c.postCSRF("/apps/blog/deployments", nil)
	// espera o build começar
	for i := 0; i < 200; i++ {
		if b, _ := os.ReadFile(p.deployLog(1)); strings.Contains(string(b), "comecou") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	pg := c.get(r.loc())
	pg.has(t, `data-log`, `data-src="/apps/blog/deployments/1/stream"`, `data-reload-on-end="1"`, "Cancelar deploy", "em andamento")
	// o stream entrega o log até agora (replay) e continua aberto: lê as primeiras linhas e cancela
	done := make(chan [][2]string, 1)
	go func() { done <- c.sse("/apps/blog/deployments/1/stream") }()
	time.Sleep(300 * time.Millisecond)
	cr := c.postCSRF("/apps/blog/deployments/1/cancel", nil)
	if cr.StatusCode != 303 {
		t.Fatalf("cancelar: %d", cr.StatusCode)
	}
	select {
	case evs := <-done:
		var text []string
		for _, e := range evs {
			text = append(text, e[1])
		}
		all := strings.Join(text, "\n")
		if !strings.Contains(all, "comecou") || evs[len(evs)-1][0] != "end" || !strings.Contains(all, `"status":"cancelled"`) {
			t.Fatalf("stream:\n%s", all)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("o stream não terminou depois do cancelamento")
	}
	if d := p.waitDeploy(1); d.Status != "cancelled" {
		t.Fatalf("%s", d.Status)
	}
	c.get(r.loc()).has(t, "cancelado")
	// cancelar de novo: mensagem amigável, sem erro
	c.postCSRF("/apps/blog/deployments/1/cancel", nil)
	c.get("/apps/blog/deployments/1").has(t, "não está em andamento")
}

func (p *panel) deployLog(id int64) string {
	d, _ := p.env.Srv.Store.Deploy(p.t.Context(), id)
	return d.LogPath
}

func TestMaliciousLogsAreInert(t *testing.T) { // AT-28
	needGit(t)
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	payload := `<script>alert(1)</script><img src=x onerror=alert(2)>` + "\\033[31mVERMELHO\\033[0m\\033]0;titulo\\007fim"
	p.env.PushSite(t, "blog", map[string]string{"index.html": "x", "droidpaas.toml": "runtime = \"static\"\nbuild = 'printf \"" + payload + "\\n\"'\n"})
	c.postCSRF("/apps/blog/deployments", nil)
	if d := p.waitDeploy(1); d.Status != "succeeded" {
		t.Fatalf("%s %v", d.Status, *d.Error)
	}
	raw, _ := os.ReadFile(p.deployLog(1))
	if !strings.Contains(string(raw), "\x1b[31m") {
		t.Fatalf("o fixture deveria gerar ANSI de verdade no arquivo de log: %q", raw)
	}
	pg := c.get("/apps/blog/deployments/1")
	pg.has(t, "&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;img src=x onerror=alert(2)&gt;VERMELHOfim")
	pg.lacks(t, "<script>alert", "<img src=x", "\x1b")
	// o mesmo pelo stream SSE: JSON com texto já limpo (nenhum ESC, nem escapado como \u001b)
	for _, e := range c.sse("/apps/blog/deployments/1/stream") {
		if strings.Contains(e[1], `\u001b`) || strings.Contains(e[1], "\x1b") || strings.Contains(e[1], `\u0007`) {
			t.Fatalf("controle no stream: %s", e[1])
		}
	}
	// logs de runtime: mesma limpeza na renderização inicial (noscript), no stream e no download
	logDir := p.env.Srv.Apps.LogDir("blog")
	os.MkdirAll(logDir, 0o700)
	os.WriteFile(filepath.Join(logDir, "current"), []byte("2026-10-10_01:00:00.00000 <b>negrito</b> \x1b[1mfim\x1b[0m\n"), 0o600)
	lp := c.get("/apps/blog/logs")
	lp.has(t, "&lt;b&gt;negrito&lt;/b&gt; fim")
	lp.lacks(t, "<b>negrito", "\x1b")
	dl := c.get("/apps/blog/logs/download")
	if dl.StatusCode != 200 || !strings.Contains(dl.Body, "<b>negrito</b> fim") || strings.Contains(dl.Body, "\x1b") ||
		!strings.Contains(dl.Header.Get("Content-Disposition"), "attachment") || !strings.HasPrefix(dl.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("download: %d %v %q", dl.StatusCode, dl.Header, dl.Body)
	}
}

func TestRuntimeLogsStreamLive(t *testing.T) { // AT-25
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	logDir := p.env.Srv.Apps.LogDir("blog")
	os.MkdirAll(logDir, 0o700)
	cur := filepath.Join(logDir, "current")
	os.WriteFile(cur, []byte("2026-10-10_01:00:00.00000 antiga\n"), 0o600)
	req, _ := http.NewRequest("GET", p.ts.URL+"/apps/blog/logs/stream", nil)
	jar := c.c.Jar
	cl := &http.Client{Jar: jar}
	rs, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Body.Close()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(rs.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				lines <- sc.Text()[6:]
			}
		}
		close(lines)
	}()
	expect := func(sub string, within time.Duration) {
		t.Helper()
		select {
		case l := <-lines:
			if !strings.Contains(l, sub) {
				t.Fatalf("esperava %q, veio %s", sub, l)
			}
		case <-time.After(within):
			t.Fatalf("a linha %q não chegou em %v", sub, within)
		}
	}
	expect("antiga", 3*time.Second)
	f, _ := os.OpenFile(cur, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("2026-10-10_01:00:05.00000 nova linha\n")
	f.Close()
	expect("nova linha", 2*time.Second) // "linhas aparecem em ≤ ~1–2 s"
}

func TestStatsFragmentAndAPI(t *testing.T) { // RF-42
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	a, _ := p.env.Srv.Apps.Get(t.Context(), "blog")
	giveRelease(t, p, a.ID)
	if _, err := p.env.Srv.Apps.Start(t.Context(), "blog"); err != nil {
		t.Fatal(err)
	}
	pid := p.env.Runit.Svcs["dp-app-blog"].PID
	p.probe.Procs[pid] = system.ProcStats{RSSKB: 58 * 1024, CPUTicks: 100, Procs: 2}
	p.col.Sample(t.Context())
	c.get("/apps/blog").has(t, "58 MB", "Memória (RSS)", "Processos")
	c.get("/apps/blog/fragments/stats").has(t, "58 MB")
	c.get("/").has(t, "58 MB")
	// API: o mesmo dado em JSON, com autenticação por token
	req, _ := http.NewRequest("GET", p.ts.URL+"/api/v1/apps/blog/stats", nil)
	req.Header.Set("Authorization", "Bearer "+p.env.Token)
	rs, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Body.Close()
	var b strings.Builder
	sc := bufio.NewScanner(rs.Body)
	for sc.Scan() {
		b.WriteString(sc.Text())
	}
	if rs.StatusCode != 200 || !strings.Contains(b.String(), `"rss_kb":59392`) {
		t.Fatalf("%d %s", rs.StatusCode, b.String())
	}
	// app parado: sem dados, sem quebrar
	p.env.Srv.Apps.Stop(t.Context(), "blog")
	p.col.Sample(t.Context())
	c.get("/apps/blog/fragments/stats").has(t, "Sem dados de uso")
}

func TestDashboardRenderIsFast(t *testing.T) { // RNF-04: ≤ 300 ms (p95) — no PC folga enorme; no aparelho é medido no e2e
	p := newPanel(t)
	c := p.loggedIn()
	for i := 0; i < 20; i++ {
		c.createApp("app-" + strconv_(i))
	}
	var worst time.Duration
	for i := 0; i < 20; i++ {
		t0 := time.Now()
		r := c.get("/")
		if d := time.Since(t0); d > worst {
			worst = d
		}
		if r.StatusCode != 200 {
			t.Fatal(r.StatusCode)
		}
	}
	if worst > 300*time.Millisecond {
		t.Fatalf("dashboard com 20 apps levou %v", worst)
	}
	t.Logf("dashboard com 20 apps: pior de 20 = %v", worst)
}

func strconv_(i int) string { return strings.TrimLeft(strings.Repeat("0", 0)+itoa(int64(i)+100), "") }
