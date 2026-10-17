package web_test

import (
	"strings"
	"testing"

	"droidpaas/internal/store"
)

func TestGitHubTab(t *testing.T) {
	p := newPanel(t)
	c := p.loggedIn()
	c.createApp("blog")
	c.get("/apps/blog").has(t, `href="/apps/blog/github"`)
	c.get("/apps/blog/github").has(t, "Publicar a partir do GitHub", `name="url"`, "Ligar ao GitHub")

	// endereço inválido: mensagem, nada gravado
	r := c.postCSRF("/apps/blog/github", form("url", "file:///etc", "branch", "main"))
	if r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	c.get("/apps/blog/github").has(t, "Endereço inválido", "Ligar ao GitHub")

	// repositório público
	c.postCSRF("/apps/blog/github", form("url", "https://github.com/acme/site", "branch", "main"))
	pg := c.get("/apps/blog/github")
	pg.has(t, "App ligado ao GitHub", "https://github.com/acme/site.git", "/hooks/github/blog", "application/json", "Just the push event", "Buscar agora e fazer deploy")
	if strings.Contains(pg.Body, "Deploy keys") {
		t.Fatal("repositório público não precisa de deploy key")
	}
	secret, _ := p.env.Srv.GitHub().Secret(t.Context(), mustApp(t, p, "blog"))
	if strings.Contains(pg.Body, secret) {
		t.Fatal("o segredo não pode vir no HTML da página")
	}
	rev := c.postCSRF("/apps/blog/github/reveal", form(), "HX-Request", "true")
	rev.has(t, secret)

	// repositório privado: mostra a deploy key (só a pública)
	c.postCSRF("/apps/blog/github", form("url", "git@github.com:acme/private.git", "branch", "release"))
	pg = c.get("/apps/blog/github")
	pg.has(t, "Passo 1: cadastre a deploy key", "ssh-ed25519 ", "droidpaas-blog", "release")
	if strings.Contains(pg.Body, "PRIVATE KEY") {
		t.Fatal("a chave privada nunca aparece no painel")
	}

	// trocar o segredo, depois desligar
	old, _ := p.env.Srv.GitHub().Secret(t.Context(), mustApp(t, p, "blog"))
	c.postCSRF("/apps/blog/github/secret", form())
	if now, _ := p.env.Srv.GitHub().Secret(t.Context(), mustApp(t, p, "blog")); now == old {
		t.Fatal("segredo não mudou")
	}
	c.postCSRF("/apps/blog/github/disconnect", form())
	c.get("/apps/blog/github").has(t, "App desligado do GitHub", "Ligar ao GitHub")

	// sem token CSRF a ação é recusada
	if r := c.post("/apps/blog/github/disconnect", form()); r.StatusCode != 403 {
		t.Fatalf("sem CSRF: %d", r.StatusCode)
	}
}

func mustApp(t *testing.T, p *panel, name string) store.App {
	t.Helper()
	a, err := p.env.Srv.Store.AppByName(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
