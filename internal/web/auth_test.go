package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUnauthenticatedIsRedirectedToLogin(t *testing.T) { // AT-20
	p := newPanel(t)
	c := p.client()
	for _, path := range []string{"/", "/apps/new", "/apps/x", "/settings", "/events", "/fragments/apps", "/apps/x/logs/stream"} {
		r := c.get(path)
		if r.StatusCode != 303 || !strings.HasPrefix(r.loc(), "/login") {
			t.Errorf("%s: %d → %q (deveria mandar para /login)", path, r.StatusCode, r.loc())
		}
	}
	if r := c.get("/"); !strings.Contains(r.loc(), "next=%2F") {
		t.Fatalf("deve lembrar o destino: %q", r.loc())
	}
	// htmx não segue redirect de página: recebe 401 + HX-Redirect
	r := c.get("/fragments/apps", "HX-Request", "true")
	if r.StatusCode != 401 || r.Header.Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx: %d %v", r.StatusCode, r.Header)
	}
	// POSTs sem sessão também não passam
	if r := c.post("/logout", url.Values{}); r.StatusCode != 303 || !strings.HasPrefix(r.loc(), "/login") {
		t.Fatalf("POST sem sessão: %d %q", r.StatusCode, r.loc())
	}
	c.get("/login").has(t, "droidpaas", `name="username"`, `name="password"`)
	if r := c.get("/static/app.css"); r.StatusCode != 200 || !strings.Contains(r.Body, "--primary") {
		t.Fatalf("assets devem ser públicos: %d", r.StatusCode)
	}
}

func TestLoginSuccessAndCookieFlags(t *testing.T) {
	p := newPanel(t)
	c := p.client()
	r := c.login("admin", adminPass)
	if r.StatusCode != 303 || r.loc() != "/" {
		t.Fatalf("%d %q", r.StatusCode, r.loc())
	}
	var ck *http.Cookie
	for _, x := range r.Cookies() {
		if x.Name == "dp_session" {
			ck = x
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.Secure {
		t.Fatalf("cookie de sessão: %+v (HttpOnly+SameSite=Lax; sem Secure em http puro)", ck)
	}
	if ck.MaxAge < 6*24*3600 {
		t.Fatalf("a sessão deve durar até 7 dias: %d", ck.MaxAge)
	}
	// o banco guarda só o hash do token (nunca o valor do cookie)
	var n int
	p.env.Srv.Store.DB.QueryRow(`select count(*) from sessions where id=?`, ck.Value).Scan(&n)
	if n != 0 {
		t.Fatal("o token do cookie não pode estar no banco em claro")
	}
	if cnt, _ := p.env.Srv.Store.CountSessions(t.Context(), 1); cnt != 1 {
		t.Fatalf("sessões = %d", cnt)
	}
	c.get("/").has(t, "Apps", "Dispositivo")
	// já logado: /login manda para o painel
	if r := c.get("/login"); r.StatusCode != 303 || r.loc() != "/" {
		t.Fatalf("%d %q", r.StatusCode, r.loc())
	}
}

func TestLoginRedirectTargets(t *testing.T) {
	p := newPanel(t)
	for next, want := range map[string]string{"/apps/x": "/apps/x", "//evil.com": "/", "https://evil.com": "/", "/\\evil.com": "/", "": "/", "/ok?a=1": "/ok?a=1"} {
		c := p.client()
		r := c.login("admin", adminPass, "?next="+url.QueryEscape(next))
		if r.loc() != want {
			t.Errorf("next=%q → %q, esperado %q (open redirect?)", next, r.loc(), want)
		}
	}
}

func TestLoginFailuresAreGenericAndRateLimited(t *testing.T) { // AT-20
	p := newPanel(t)
	c := p.client()
	for i := 1; i <= 5; i++ {
		r := c.login("admin", "errada-"+strings.Repeat("x", i))
		if r.StatusCode != 401 {
			t.Fatalf("tentativa %d: %d", i, r.StatusCode)
		}
		r.has(t, "Usuário ou senha inválidos")
	}
	// usuário inexistente: mesma resposta (não revela quem existe)
	if r := p.client().login("fantasma", "qualquer-coisa-123"); r.StatusCode != 401 || !strings.Contains(r.Body, "Usuário ou senha inválidos") {
		t.Fatalf("%d %s", r.StatusCode, r.Body)
	}
	// 6ª tentativa na mesma chave (IP+usuário): bloqueada, mesmo com a senha certa
	r := c.login("admin", adminPass)
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("esperava 429 com Retry-After: %d %v", r.StatusCode, r.Header)
	}
	r.has(t, "Muitas tentativas")
	// depois da janela de 5 min libera
	p.now.Store(time.Now().Add(6 * time.Minute).UnixMilli())
	if r := c.login("admin", adminPass); r.StatusCode != 303 {
		t.Fatalf("depois da janela deveria entrar: %d", r.StatusCode)
	}
	// eventos de auditoria sem senha
	evs, _ := p.env.Srv.Events.List(t.Context(), 50, 0)
	var failed int
	for _, e := range evs {
		if e.Kind == "auth.login_failed" {
			failed++
		}
		if strings.Contains(e.Message+string(e.Data), "errada") || strings.Contains(e.Message+string(e.Data), adminPass) {
			t.Fatalf("senha vazou na auditoria: %s", e.Message)
		}
	}
	if failed < 5 {
		t.Fatalf("falhas de login devem ser auditadas, achei %d", failed)
	}
}

func TestLoginCSRFAndOrigin(t *testing.T) {
	p := newPanel(t)
	c := p.client()
	// sem cookie/token de login
	if r := c.post("/login", url.Values{"username": {"admin"}, "password": {adminPass}}); r.StatusCode != 403 {
		t.Fatalf("sem token: %d", r.StatusCode)
	}
	pg := c.get("/login")
	m := loginTokRe.FindStringSubmatch(pg.Body)
	// token errado
	if r := c.post("/login", url.Values{"_csrf": {"errado"}, "username": {"admin"}, "password": {adminPass}}); r.StatusCode != 403 {
		t.Fatalf("token errado: %d", r.StatusCode)
	}
	// origem diferente
	if r := c.post("/login", url.Values{"_csrf": {m[1]}, "username": {"admin"}, "password": {adminPass}}, "Origin", "https://evil.example"); r.StatusCode != 403 {
		t.Fatalf("Origin externo: %d", r.StatusCode)
	}
	if cnt, _ := p.env.Srv.Store.CountSessions(t.Context(), 1); cnt != 0 {
		t.Fatal("nenhuma sessão deveria ter sido criada")
	}
}

func TestCSRFOnAuthenticatedPosts(t *testing.T) { // AT-21
	p := newPanel(t)
	c := p.loggedIn()
	tok := c.token()
	if r := c.post("/apps/new", url.Values{"step": {"1"}, "name": {"x1"}, "branch": {"main"}}); r.StatusCode != 403 {
		t.Fatalf("POST sem token: %d", r.StatusCode)
	}
	if r := c.post("/apps/new", url.Values{"_csrf": {"adulterado"}, "step": {"1"}, "name": {"x1"}}); r.StatusCode != 403 {
		t.Fatalf("token adulterado: %d", r.StatusCode)
	}
	if r := c.post("/apps/new", url.Values{"_csrf": {tok}, "step": {"1"}, "name": {"x1"}, "branch": {"main"}}, "Origin", "https://evil.example"); r.StatusCode != 403 {
		t.Fatalf("token certo mas Origin externo: %d", r.StatusCode)
	}
	if r := c.post("/apps/new", url.Values{"_csrf": {tok}, "step": {"1"}, "name": {"x1"}, "branch": {"main"}}, "Referer", "https://evil.example/pagina"); r.StatusCode != 403 {
		t.Fatalf("Referer externo: %d", r.StatusCode)
	}
	if r := c.post("/apps/new", url.Values{"_csrf": {tok}, "step": {"1"}, "name": {"x1"}, "branch": {"main"}}, "Origin", p.ts.URL); r.StatusCode != 200 {
		t.Fatalf("mesma origem + token: %d", r.StatusCode)
	}
	// o token também vale pelo cabeçalho (htmx)
	if r := c.post("/apps/new", url.Values{"step": {"1"}, "name": {"x2"}, "branch": {"main"}}, "X-CSRF-Token", tok); r.StatusCode != 200 {
		t.Fatalf("cabeçalho X-CSRF-Token: %d", r.StatusCode)
	}
	// a recusa é auditada
	evs, _ := p.env.Srv.Events.List(t.Context(), 20, 0)
	found := false
	for _, e := range evs {
		found = found || e.Kind == "auth.csrf_rejected"
	}
	if !found {
		t.Fatal("CSRF recusado deve gerar evento")
	}
	// GET nunca altera estado
	if r := c.get("/apps/new"); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
}

func TestSessionExpiry(t *testing.T) { // AT-20: 12 h de inatividade / 7 dias absolutos
	p := newPanel(t)
	c := p.loggedIn()
	if r := c.get("/"); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	base := time.Now()
	// 11 h depois: ainda vale (e renova a inatividade)
	p.now.Store(base.Add(11 * time.Hour).UnixMilli())
	if r := c.get("/"); r.StatusCode != 200 {
		t.Fatalf("11 h: %d", r.StatusCode)
	}
	// +11 h (22 h desde o login, 11 h desde o último uso): vale
	p.now.Store(base.Add(22 * time.Hour).UnixMilli())
	if r := c.get("/"); r.StatusCode != 200 {
		t.Fatalf("inatividade de 11 h: %d", r.StatusCode)
	}
	// +13 h sem usar: expirou por inatividade
	p.now.Store(base.Add(35 * time.Hour).UnixMilli())
	if r := c.get("/"); r.StatusCode != 303 {
		t.Fatalf("inatividade de 13 h deveria expirar: %d", r.StatusCode)
	}
	if cnt, _ := p.env.Srv.Store.CountSessions(t.Context(), 1); cnt != 0 {
		t.Fatal("sessão expirada deve ser apagada do banco")
	}
	// expiração absoluta: usando a cada 10 h, ainda assim cai aos 7 dias
	p.now.Store(0)
	c2 := p.loggedIn()
	t0 := time.Now()
	for h := 10; h < 7*24; h += 10 {
		p.now.Store(t0.Add(time.Duration(h) * time.Hour).UnixMilli())
		if r := c2.get("/"); r.StatusCode != 200 {
			t.Fatalf("%d h de uso contínuo: %d", h, r.StatusCode)
		}
	}
	p.now.Store(t0.Add(7*24*time.Hour + time.Hour).UnixMilli())
	if r := c2.get("/"); r.StatusCode != 303 {
		t.Fatalf("7 dias absolutos: %d", r.StatusCode)
	}
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	// guarda o cookie para tentar reutilizá-lo depois do logout (replay)
	u, _ := url.Parse(p.ts.URL)
	saved := c.c.Jar.Cookies(u)
	r := c.postCSRF("/logout", nil)
	if r.StatusCode != 303 || r.loc() != "/login" {
		t.Fatalf("%d %q", r.StatusCode, r.loc())
	}
	if r := c.get("/"); r.StatusCode != 303 {
		t.Fatal("depois do logout o painel exige login")
	}
	replay := p.client()
	replay.c.Jar.SetCookies(u, saved)
	if r := replay.get("/"); r.StatusCode != 303 {
		t.Fatalf("o cookie antigo não pode voltar a valer: %d", r.StatusCode)
	}
}

func TestSecureCookieBehindTrustedProxy(t *testing.T) {
	p := newPanel(t)
	get := func(hdr ...string) *http.Cookie {
		c := p.client()
		pg := c.get("/login")
		m := loginTokRe.FindStringSubmatch(pg.Body)
		r := c.post("/login", url.Values{"_csrf": {m[1]}, "username": {"admin"}, "password": {adminPass}}, hdr...)
		for _, ck := range r.Cookies() {
			if ck.Name == "dp_session" {
				return ck
			}
		}
		t.Fatalf("sem cookie: %d", r.StatusCode)
		return nil
	}
	if ck := get("X-Forwarded-Proto", "https"); !ck.Secure { // 127.0.0.1 é proxy confiável por padrão
		t.Fatal("atrás do túnel (X-Forwarded-Proto: https de proxy confiável) o cookie deve ser Secure")
	}
	if ck := get(); ck.Secure {
		t.Fatal("http puro: sem Secure")
	}
	p.env.Srv.Cfg.HTTP.TrustedProxies = nil // quem não é confiável não pode forjar o cabeçalho
	if ck := get("X-Forwarded-Proto", "https"); ck.Secure {
		t.Fatal("cabeçalho de par não confiável deve ser ignorado")
	}
	p.env.Srv.Cfg.HTTP.SecureCookies = "always"
	if ck := get(); !ck.Secure {
		t.Fatal("secure_cookies=always")
	}
	p.env.Srv.Cfg.HTTP.SecureCookies = "never"
	p.env.Srv.Cfg.HTTP.TrustedProxies = []string{"127.0.0.1"}
	if ck := get("X-Forwarded-Proto", "https"); ck.Secure {
		t.Fatal("secure_cookies=never")
	}
}

func TestLoginRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	p := newPanel(t)
	for i := 1; i <= 5; i++ { // o cliente forja o início do cabeçalho; o túnel acrescenta o IP real no fim
		c := p.client()
		pg := c.get("/login")
		m := loginTokRe.FindStringSubmatch(pg.Body)
		r := c.post("/login", url.Values{"_csrf": {m[1]}, "username": {"admin"}, "password": {"errada-" + strings.Repeat("x", i)}},
			"X-Forwarded-For", "10.0.0."+strings.Repeat("1", i)+", 203.0.113.7")
		if r.StatusCode != 401 {
			t.Fatalf("tentativa %d: %d", i, r.StatusCode)
		}
	}
	c := p.client()
	pg := c.get("/login")
	m := loginTokRe.FindStringSubmatch(pg.Body)
	r := c.post("/login", url.Values{"_csrf": {m[1]}, "username": {"admin"}, "password": {adminPass}}, "X-Forwarded-For", "10.9.9.9, 203.0.113.7")
	if r.StatusCode != 429 {
		t.Fatalf("IP forjado no início do X-Forwarded-For burlou o limite: %d", r.StatusCode)
	}
}
