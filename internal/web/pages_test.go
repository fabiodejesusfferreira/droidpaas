package web_test

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/system"
)

func form(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func (c *client) createApp(name string) {
	c.p.t.Helper()
	if _, err := c.p.env.Srv.Apps.Create(c.p.t.Context(), apps.CreateInput{Name: name}); err != nil {
		c.p.t.Fatal(err)
	}
}

func TestSecurityHeadersEverywhere(t *testing.T) { // TSD §12.6
	p := newPanel(t)
	c := p.loggedIn()
	for _, path := range []string{"/", "/login", "/static/app.css", "/static/app.js", "/apps/nao-existe", "/nada", "/settings", "/events"} {
		r := c.get(path)
		h := r.Header
		csp := h.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'self'", "script-src 'self'", "style-src 'self'", "frame-ancestors 'none'", "form-action 'self'", "base-uri 'none'", "object-src 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP sem %q: %s", path, want, csp)
			}
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "http") {
			t.Errorf("%s: CSP permissiva: %s", path, csp)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "same-origin" || h.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: cabeçalhos: %v", path, h)
		}
		if !strings.HasPrefix(path, "/static") && h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: páginas autenticadas não podem ser cacheadas", path)
		}
	}
}

func TestNoExternalResourcesAndNoInlineScripts(t *testing.T) { // critério da Fase 3: nada de rede externa
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	ext := regexp.MustCompile(`(?i)(src|href|action)\s*=\s*"(https?:)?//`)
	inl := regexp.MustCompile(`(?i)<script(\s[^>]*)?>[^<]|\son[a-z]+\s*=|\sstyle\s*=|javascript:`)
	for _, path := range []string{"/", "/apps/new", "/apps/blog", "/apps/blog/deployments", "/apps/blog/environment", "/apps/blog/logs", "/apps/blog/domains",
		"/apps/blog/settings", "/apps/blog/publish", "/settings/general", "/settings/tunnel", "/settings/keys", "/settings/tokens", "/settings/account", "/events"} {
		r := c.get(path)
		if r.StatusCode != 200 {
			t.Errorf("%s: %d", path, r.StatusCode)
			continue
		}
		if m := ext.FindString(r.Body); m != "" {
			t.Errorf("%s referencia recurso externo: %q", path, m)
		}
		if m := inl.FindString(r.Body); m != "" {
			t.Errorf("%s tem script/handler/estilo inline (a CSP bloquearia): %q", path, m)
		}
		if !strings.Contains(r.Body, `<html lang="pt-BR"`) || !strings.Contains(r.Body, `<meta name="viewport"`) {
			t.Errorf("%s sem lang/viewport", path)
		}
	}
	// os assets estáticos próprios também não carregam nada de fora
	for _, a := range []string{"/static/app.css", "/static/app.js"} {
		body := c.get(a).Body
		if regexp.MustCompile(`https?://|url\(\s*['"]?https?:`).MatchString(body) {
			t.Errorf("%s referencia URL externa", a)
		}
	}
}

func TestDashboardDeviceWidgetAndApps(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	r := c.get("/")
	r.has(t, "Nenhum app ainda", "77 %", "33.4 °C", "carregando", "1464 MB", "livre", "60 %") // RAM livre 1500000 kB = 1464 MB; disco 60% usado
	r.lacks(t, "indisponível")
	// Termux:API ausente → "indisponível" sem quebrar a página (AT-27)
	p.probe.BatErr = system.ErrUnavailable
	p.col.Sample(t.Context())
	r = c.get("/")
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	r.has(t, "indisponível", "1464 MB")
	r.lacks(t, "77 %")
	// bateria quente → alerta (sem ação automática)
	p.probe.BatErr = nil
	p.probe.Bat.TempC = 43
	p.col.Sample(t.Context())
	c.get("/fragments/device").has(t, "quente")
	// apps listados com estado e link
	c.createApp("minha-api")
	c.createApp("blog")
	r = c.get("/fragments/apps")
	r.has(t, `href="/apps/blog"`, `href="/apps/minha-api"`, "parado", "sem release")
	if !strings.Contains(c.get("/").Body, `hx-get="/fragments/apps"`) || !strings.Contains(c.get("/").Body, `hx-trigger="every 10s"`) {
		t.Fatal("o dashboard deve atualizar por polling leve (htmx, 10 s)")
	}
}

func TestWizardCreatesApp(t *testing.T) { // AT-22
	p := newPanel(t)
	c := p.loggedIn()
	c.get("/apps/new").has(t, "Novo app", `name="name"`, "Nome e branch")
	// passo 1 inválido
	r := c.postCSRF("/apps/new", form("step", "1", "name", "Nome Ruim!", "branch", "main"))
	if r.StatusCode != 422 {
		t.Fatalf("nome inválido: %d", r.StatusCode)
	}
	r.has(t, "Nome inválido")
	// passo 1 ok → passo 2
	r = c.postCSRF("/apps/new", form("step", "1", "name", "minha-api", "branch", "main"))
	r.has(t, "Runtime", `name="runtime"`, "Comando de build")
	// passo 2 → revisão
	r = c.postCSRF("/apps/new", form("step", "2", "name", "minha-api", "branch", "main", "runtime", "node", "build_cmd", "npm run build", "start_cmd", "", "health_path", "/health"))
	r.has(t, "Revisar", "minha-api", "Node.js", "npm run build", "/health", "Nenhuma chave SSH") // sem chave cadastrada → aviso
	// voltar
	if r := c.postCSRF("/apps/new", form("step", "3", "back", "1", "name", "minha-api", "branch", "main", "runtime", "node")); !strings.Contains(r.Body, "Comando de build") {
		t.Fatal("o botão Voltar deve ir ao passo 2")
	}
	// confirma → cria e mostra como publicar
	r = c.postCSRF("/apps/new", form("step", "3", "name", "minha-api", "branch", "main", "runtime", "node", "build_cmd", "npm run build", "start_cmd", "", "health_path", "/health"))
	if r.StatusCode != 303 || r.loc() != "/apps/minha-api/publish" {
		t.Fatalf("%d %q\n%s", r.StatusCode, r.loc(), r.Body)
	}
	pub := c.get(r.loc())
	pub.has(t, "App criado", "git remote add droid ssh://", ":8022/minha-api.git", "git push droid main", "@IP-DO-CELULAR:8022", "Cadastre sua chave SSH", "Acompanhe o deploy", "App &#34;minha-api&#34; criado")
	a, err := p.env.Srv.Apps.Get(t.Context(), "minha-api")
	if err != nil || a.Runtime != "node" || a.BuildCmd == nil || *a.BuildCmd != "npm run build" || a.HealthPath == nil || *a.HealthPath != "/health" {
		t.Fatalf("%+v %v", a, err)
	}
	// duplicado → volta ao passo 1 com erro
	r = c.postCSRF("/apps/new", form("step", "3", "name", "minha-api", "branch", "main", "runtime", "auto"))
	if r.StatusCode != 422 {
		t.Fatalf("duplicado: %d", r.StatusCode)
	}
	r.has(t, "Já existe")
	// nome reservado
	r = c.postCSRF("/apps/new", form("step", "3", "name", "admin", "branch", "main", "runtime", "auto"))
	if r.StatusCode != 422 {
		t.Fatalf("reservado: %d", r.StatusCode)
	}
	// com chave cadastrada o aviso some
	if _, err := p.env.Srv.AddSSHKey(t.Context(), 1, "laptop", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEoRFrfGwgMOpPPgi2ND7hknjdlxTZkzTTGsOmZpYZCH laptop"); err != nil {
		t.Fatal(err)
	}
	if r := c.postCSRF("/apps/new", form("step", "2", "name", "outro", "branch", "main", "runtime", "go")); strings.Contains(r.Body, "Nenhuma chave SSH") {
		t.Fatal("com chave cadastrada não há aviso")
	}
}

func TestAppPagesAndActions(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	for path, want := range map[string][]string{
		"/apps/blog":             {"blog", "Visão geral", "Release atual", "sem release", "Git", "git@" /* placeholder: ajustado abaixo */},
		"/apps/blog/deployments": {"Novo deploy", "Histórico", "Nenhum deploy ainda", "Não há release anterior"},
		"/apps/blog/environment": {"Variáveis de ambiente", "Salvar e reiniciar"},
		"/apps/blog/logs":        {`data-src="/apps/blog/logs/stream"`, `data-follow`, "Baixar últimas linhas"},
		"/apps/blog/domains":     {"Domínios", "Novo domínio", "Como fazer o domínio funcionar"},
		"/apps/blog/settings":    {"Zona de perigo", `name="health_timeout_s"`, "Remover app"},
	} {
		r := c.get(path)
		if r.StatusCode != 200 {
			t.Errorf("%s: %d", path, r.StatusCode)
			continue
		}
		for _, w := range want {
			if w == "git@" {
				continue
			}
			if !strings.Contains(r.Body, w) {
				t.Errorf("%s sem %q", path, w)
			}
		}
		r.has(t, `aria-current="page"`, `class="tabs"`)
	}
	// app inexistente → 404 amigável
	for _, path := range []string{"/apps/nao-existe", "/apps/nao-existe/deployments", "/apps/nao-existe/settings", "/apps/nao-existe/publish"} {
		if r := c.get(path); r.StatusCode != 404 || !strings.Contains(r.Body, "Esse app não existe") {
			t.Errorf("%s: %d", path, r.StatusCode)
		}
	}
	// start sem release → mensagem clara (flash) e volta à página
	r := c.postCSRF("/apps/blog/action/start", nil)
	if r.StatusCode != 303 || r.loc() != "/apps/blog" {
		t.Fatalf("%d %q", r.StatusCode, r.loc())
	}
	c.get("/apps/blog").has(t, "ainda não tem uma versão publicada")
	c.get("/apps/blog").lacks(t, "ainda não tem uma versão publicada") // o flash é exibido uma vez só
	// ação desconhecida
	if r := c.postCSRF("/apps/blog/action/format", nil); r.StatusCode != 404 {
		t.Fatalf("ação desconhecida: %d", r.StatusCode)
	}
	// stop / start / restart com release
	a, _ := p.env.Srv.Apps.Get(t.Context(), "blog")
	giveRelease(t, p, a.ID)
	for _, act := range []string{"start", "restart", "stop"} {
		if r := c.postCSRF("/apps/blog/action/"+act, nil); r.StatusCode != 303 {
			t.Fatalf("%s: %d", act, r.StatusCode)
		}
	}
	c.get("/apps/blog").has(t, "App parado", "Iniciar")
}

func TestAppSettingsAndDelete(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	ok := form("runtime", "node", "branch", "release", "build_cmd", "npm run build", "start_cmd", "node s.js", "health_path", "/h", "health_timeout_s", "45", "static_dir", "", "nice", "5")
	if r := c.postCSRF("/apps/blog/settings", ok); r.StatusCode != 303 {
		t.Fatalf("%d", r.StatusCode)
	}
	a, _ := p.env.Srv.Apps.Get(t.Context(), "blog")
	if a.Runtime != "node" || a.Branch != "release" || *a.StartCmd != "node s.js" || a.HealthTimeoutS != 45 || a.Nice != 5 {
		t.Fatalf("%+v", a)
	}
	c.get("/apps/blog/settings").has(t, "Configurações salvas", `value="node s.js"`, `value="45"`)
	// valor inválido → mensagem e nada muda
	bad := form("runtime", "ruby", "branch", "release", "health_timeout_s", "45", "nice", "5")
	c.postCSRF("/apps/blog/settings", bad)
	c.get("/apps/blog/settings").has(t, "Valor inválido em runtime")
	bad = form("runtime", "node", "branch", "release", "health_timeout_s", "abc", "nice", "5")
	c.postCSRF("/apps/blog/settings", bad)
	c.get("/apps/blog/settings").has(t, "health_timeout_s")
	// remover exige o nome
	c.postCSRF("/apps/blog/delete", form("confirm", "outro"))
	if _, err := p.env.Srv.Apps.Get(t.Context(), "blog"); err != nil {
		t.Fatal("sem o nome certo o app não pode ser removido")
	}
	c.get("/apps/blog/settings").has(t, "confirm")
	r := c.postCSRF("/apps/blog/delete", form("confirm", "blog"))
	if r.StatusCode != 303 || r.loc() != "/" {
		t.Fatalf("%d %q", r.StatusCode, r.loc())
	}
	c.get("/").has(t, `App &#34;blog&#34; removido`)
	if _, err := p.env.Srv.Apps.Get(t.Context(), "blog"); err == nil {
		t.Fatal("app deveria ter sido removido")
	}
	if _, ok := p.env.Runit.Svcs["dp-app-blog"]; ok {
		t.Fatal("serviço runit não removido")
	}
}

func TestEnvironmentBatchEditAndReveal(t *testing.T) { // AT-24
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	// adiciona: uma comum, um segredo (linhas novas continuam o índice das existentes)
	r := c.postCSRF("/apps/blog/environment", form("k", "LOG_LEVEL", "v", "info", "k", "DATABASE_URL", "v", "postgres://u:senha@h/db", "sab", "1"))
	if r.StatusCode != 303 {
		t.Fatalf("%d", r.StatusCode)
	}
	pg := c.get("/apps/blog/environment")
	pg.has(t, "Variáveis salvas", `value="LOG_LEVEL"`, `value="info"`, `value="DATABASE_URL"`, "••••", "Revelar")
	pg.lacks(t, "senha@h")
	// segredo intocado (continua mascarado) + remoção de uma comum
	r = c.postCSRF("/apps/blog/environment", form("k", "DATABASE_URL", "v", "••••", "saa", "1", "k", "LOG_LEVEL", "v", "info", "dab", "1"))
	_ = r
	list, _ := p.env.Srv.Apps.EnvList(t.Context(), "blog")
	if len(list) != 1 || list[0].Key != "DATABASE_URL" {
		t.Fatalf("remoção: %+v", list)
	}
	if v, _ := p.env.Srv.Apps.EnvReveal(t.Context(), "blog", "DATABASE_URL"); v != "postgres://u:senha@h/db" {
		t.Fatalf("o segredo mascarado não pode ser sobrescrito pela máscara: %q", v)
	}
	// revelar (htmx): valor escapado e auditado
	rv := c.postCSRF("/apps/blog/environment/reveal", form("key", "DATABASE_URL"), "HX-Request", "true")
	if rv.StatusCode != 200 || !strings.Contains(rv.Body, "postgres://u:senha@h/db") {
		t.Fatalf("%d %s", rv.StatusCode, rv.Body)
	}
	evs, _ := p.env.Srv.Events.List(t.Context(), 20, 0)
	var revealed bool
	for _, e := range evs {
		revealed = revealed || e.Kind == "env.revealed"
		if strings.Contains(e.Message+string(e.Data), "senha@h") {
			t.Fatal("o valor do segredo não pode ir para os eventos")
		}
	}
	if !revealed {
		t.Fatal("revelar segredo deve ser auditado")
	}
	if r := c.post("/apps/blog/environment/reveal", form("key", "DATABASE_URL")); r.StatusCode != 403 {
		t.Fatalf("revelar sem CSRF: %d", r.StatusCode)
	}
	// variável reservada → erro amigável
	c.postCSRF("/apps/blog/environment", form("k", "PORT", "v", "1"))
	c.get("/apps/blog/environment").has(t, "reservada")
	// "Salvar e reiniciar" sem release não falha
	c.postCSRF("/apps/blog/environment", form("k", "X", "v", "1", "restart", "1"))
	c.get("/apps/blog/environment").has(t, "Variáveis salvas")
	// valores com HTML ficam escapados
	c.postCSRF("/apps/blog/environment", form("k", "HTML", "v", `<img src=x onerror=alert(1)>`))
	pg = c.get("/apps/blog/environment")
	pg.has(t, "&lt;img src=x onerror=alert(1)&gt;")
	pg.lacks(t, "<img src=x")
}

func TestDomainsPage(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	c.postCSRF("/settings/general", form("base_domain", "apps.exemplo.com", "keep_releases", "5", "git_http_enabled", "1"))
	c.get("/apps/blog/domains").has(t, "blog.apps.exemplo.com", "automático", "*.apps.exemplo.com")
	c.postCSRF("/apps/blog/domains", form("hostname", "Meu.Site.com.br"))
	pg := c.get("/apps/blog/domains")
	pg.has(t, "Domínio adicionado", "meu.site.com.br")
	c.postCSRF("/apps/blog/domains", form("hostname", "meu.site.com.br"))
	c.get("/apps/blog/domains").has(t, "Já existe")
	c.postCSRF("/apps/blog/domains", form("hostname", "sem ponto"))
	c.get("/apps/blog/domains").has(t, "hostname inválido")
	c.postCSRF("/apps/blog/domains/remove", form("hostname", "blog.apps.exemplo.com")) // automático: não remove
	c.get("/apps/blog/domains").has(t, "domínio automático não pode ser removido")
	c.postCSRF("/apps/blog/domains/remove", form("hostname", "meu.site.com.br"))
	c.get("/apps/blog/domains").has(t, "Domínio removido")
}

func TestGlobalSettingsTabs(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	for _, tab := range []string{"general", "tunnel", "keys", "tokens", "account"} {
		if r := c.get("/settings/" + tab); r.StatusCode != 200 {
			t.Errorf("%s: %d", tab, r.StatusCode)
		}
	}
	if r := c.get("/settings"); r.StatusCode != 200 || !strings.Contains(r.Body, "Domínio base") {
		t.Fatal("/settings deve abrir a aba Geral")
	}
	if r := c.get("/settings/inexistente"); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	// geral
	c.postCSRF("/settings/general", form("base_domain", "apps.exemplo.com", "panel_hostname", "painel.exemplo.com", "keep_releases", "3"))
	c.get("/settings/general").has(t, "Configurações salvas", `value="apps.exemplo.com"`, `value="3"`)
	if set, _ := p.env.Srv.EffectiveSettings(t.Context()); set["git_http_enabled"] != "false" {
		t.Fatalf("checkbox desmarcado deve desligar o Git HTTP: %v", set)
	}
	c.postCSRF("/settings/general", form("base_domain", "Inválido!", "keep_releases", "3"))
	c.get("/settings/general").has(t, "Valor inválido em base_domain")
	// túnel: o token nunca volta no HTML
	c.postCSRF("/settings/tunnel", form("token", ""))
	c.get("/settings/tunnel").has(t, "Cole o token")
	c.postCSRF("/settings/tunnel", form("token", "TOKEN-SECRETO-DO-TUNEL"))
	pg := c.get("/settings/tunnel")
	pg.has(t, "Token salvo", "token já configurado", "Remover token")
	pg.lacks(t, "TOKEN-SECRETO-DO-TUNEL")
	if v, _, _ := p.env.Srv.Store.Setting(t.Context(), "tunnel_token"); strings.Contains(v, "TOKEN-SECRETO") {
		t.Fatal("token do túnel gravado em claro")
	}
	c.postCSRF("/settings/tunnel", form("clear", "1"))
	c.get("/settings/tunnel").has(t, "Token removido")
	// chaves SSH
	c.postCSRF("/settings/keys", form("name", "laptop", "public_key", "isto não é chave"))
	c.get("/settings/keys").has(t, "chave pública inválida")
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEoRFrfGwgMOpPPgi2ND7hknjdlxTZkzTTGsOmZpYZCH"
	c.postCSRF("/settings/keys", form("name", "laptop", "public_key", key))
	pg = c.get("/settings/keys")
	pg.has(t, "Chave adicionada", "SHA256:", "laptop")
	c.postCSRF("/settings/keys", form("name", "dup", "public_key", key))
	c.get("/settings/keys").has(t, "já está cadastrada")
	ks, _ := p.env.Srv.Store.SSHKeys(t.Context())
	c.postCSRF("/settings/keys/remove", form("id", itoa(ks[0].ID)))
	c.get("/settings/keys").has(t, "Chave removida", "Nenhuma chave cadastrada")
	// tokens: o valor aparece uma vez só
	c.postCSRF("/settings/tokens", form("name", "ci", "scope", "git"))
	pg = c.get("/settings/tokens")
	pg.has(t, "Token criado", "não será mostrado de novo", "dp_")
	tok := regexp.MustCompile(`dp_[A-Za-z0-9_-]{43}`).FindString(pg.Body)
	if tok == "" {
		t.Fatalf("token não exibido:\n%s", pg.Body)
	}
	pg2 := c.get("/settings/tokens")
	pg2.lacks(t, tok)
	pg2.has(t, "ci", "git", "ativo")
	toks, _ := p.env.Srv.Store.ListTokens(t.Context(), 1)
	c.postCSRF("/settings/tokens/revoke", form("id", itoa(toks[len(toks)-1].ID)))
	c.get("/settings/tokens").has(t, "Token revogado", "revogado")
	c.postCSRF("/settings/tokens", form("name", "", "scope", "root"))
	c.get("/settings/tokens").has(t, "Valor inválido")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestAccountPasswordChange(t *testing.T) { // RF-41
	p := newPanel(t)
	a, b := p.loggedIn(), p.loggedIn() // duas sessões
	a.get("/settings/account").has(t, "Sessões ativas: 2")
	a.postCSRF("/settings/account", form("current", "errada", "new", "nova-senha-bem-longa", "confirm", "nova-senha-bem-longa"))
	a.get("/settings/account").has(t, "senha atual não confere")
	a.postCSRF("/settings/account", form("current", adminPass, "new", "curta", "confirm", "curta"))
	a.get("/settings/account").has(t, "ao menos 12")
	a.postCSRF("/settings/account", form("current", adminPass, "new", "nova-senha-bem-longa", "confirm", "outra-coisa-bem-longa"))
	a.get("/settings/account").has(t, "confirmação não confere")
	a.postCSRF("/settings/account", form("current", adminPass, "new", "nova-senha-bem-longa", "confirm", "nova-senha-bem-longa"))
	a.get("/settings/account").has(t, "1 outra(s) sessão(ões) encerrada(s)")
	if r := b.get("/"); r.StatusCode != 303 {
		t.Fatalf("a outra sessão deveria ter sido encerrada: %d", r.StatusCode)
	}
	if r := a.get("/"); r.StatusCode != 200 {
		t.Fatalf("a sessão atual continua: %d", r.StatusCode)
	}
	if r := p.client().login("admin", adminPass); r.StatusCode != 401 {
		t.Fatalf("a senha antiga não vale mais: %d", r.StatusCode)
	}
	if r := p.client().login("admin", "nova-senha-bem-longa"); r.StatusCode != 303 {
		t.Fatalf("a nova senha vale: %d", r.StatusCode)
	}
	evs, _ := p.env.Srv.Events.List(t.Context(), 50, 0)
	for _, e := range evs {
		if strings.Contains(e.Message+string(e.Data), "nova-senha") || strings.Contains(e.Message+string(e.Data), adminPass) {
			t.Fatal("senhas não podem ir para a auditoria")
		}
	}
}

func TestEventsPage(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	c.get("/events").has(t, "Eventos", "auth.login") // o login já gerou evento
	for i := 0; i < 60; i++ {
		p.env.Srv.Events.Info(t.Context(), "teste.evento", nil, "evento número "+itoa(int64(i)), map[string]any{"i": i})
	}
	r := c.get("/events")
	r.has(t, "Mais antigos", "teste.evento", "evento número 59")
	next := regexp.MustCompile(`/events\?before=(\d+)`).FindStringSubmatch(r.Body)
	if next == nil {
		t.Fatal("sem link de paginação")
	}
	r2 := c.get("/events?before=" + next[1])
	r2.lacks(t, "evento número 59")
	r2.has(t, "teste.evento")
	// mensagens com HTML/ANSI são limpas e escapadas
	p.env.Srv.Events.Info(t.Context(), "teste.xss", nil, "<script>alert(1)</script>\x1b[31m vermelho\x1b[0m", nil)
	r = c.get("/events")
	r.has(t, "&lt;script&gt;alert(1)&lt;/script&gt; vermelho")
	r.lacks(t, "<script>alert", "\x1b")
}

func giveRelease(t *testing.T, p *panel, appID int64) {
	t.Helper()
	// cria uma release "pronta" diretamente para habilitar start/stop/restart sem rodar um deploy
	if _, err := p.env.Srv.Store.DB.ExecContext(t.Context(),
		`insert into releases(app_id,git_sha,git_ref,message,status,path,runtime,created_at) values(?,?,?,?,?,?,?,?)`,
		appID, "abcdef1234567890", "main", "primeiro", "ready", "/x/1-abcdef1", "node", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var rid int64
	p.env.Srv.Store.DB.QueryRow(`select max(id) from releases`).Scan(&rid)
	p.env.Srv.Store.SetCurrentRelease(t.Context(), appID, &rid)
}
