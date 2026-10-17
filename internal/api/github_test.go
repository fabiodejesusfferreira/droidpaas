package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"droidpaas/internal/ghsrc"
)

// newOrigin cria um repositório bare com um site estático: faz o papel do GitHub.
func newOrigin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	origin, work := filepath.Join(t.TempDir(), "origin.git"), t.TempDir()
	run := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(work, "init", "-q", "--bare", "-b", "main", origin)
	w := t.TempDir()
	run(w, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(w, "index.html"), []byte("<h1>do github</h1>"), 0o644)
	run(w, "add", ".")
	run(w, "commit", "-qm", "site")
	run(w, "push", "-q", origin, "main")
	return origin
}

// bareRepo cria o repo.git do app (o api.newEnv não monta o gitsrv, que normalmente faz isso em Apps.Create).
func (e *env) bareRepo(t *testing.T, app string) {
	t.Helper()
	repo := filepath.Join(e.srv.Apps.AppDir(app), "repo.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func (e *env) hook(t *testing.T, app, event, delivery, secret, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+"/hooks/github/"+app, strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	if secret != "" {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(body))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(m.Sum(nil)))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

func TestGitHubConnectValidation(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})
	for _, bad := range []string{"file:///etc", "https://evil.com/a/b", "--upload-pack=x", ""} {
		if code, _, _ := e.do(t, "PUT", "/api/v1/apps/blog/github", e.token, map[string]string{"url": bad}); code != 400 {
			t.Errorf("%q → %d", bad, code)
		}
	}
	if code, _, _ := e.do(t, "PUT", "/api/v1/apps/blog/github", e.token, map[string]string{"url": "https://github.com/a/b", "branch": "--force"}); code != 400 {
		t.Fatal("branch inválida deveria ser 400")
	}
	if code, _, _ := e.do(t, "GET", "/api/v1/apps/blog/github", "", nil); code != 401 {
		t.Fatalf("API de configuração exige token: %d", code)
	}
	code, m, _ := e.do(t, "GET", "/api/v1/apps/blog/github", e.token, nil)
	if code != 200 || m["connected"] != false {
		t.Fatal(code, m)
	}

	code, m, _ = e.do(t, "PUT", "/api/v1/apps/blog/github", e.token, map[string]string{"url": "https://github.com/acme/site"})
	if code != 200 || m["private"] != false || len(m["secret"].(string)) != 64 || !strings.HasSuffix(m["webhook_url"].(string), "/hooks/github/blog") || m["branch"] != "main" {
		t.Fatal(code, m)
	}
	_, m, _ = e.do(t, "GET", "/api/v1/apps/blog/github", e.token, nil)
	if _, has := m["secret"]; has {
		t.Fatal("o segredo não pode vir sem reveal=1")
	}
	code, m, _ = e.do(t, "PUT", "/api/v1/apps/blog/github", e.token, map[string]string{"url": "git@github.com:acme/private.git", "branch": "release"})
	if code != 200 || m["private"] != true || !strings.HasPrefix(m["deploy_key"].(string), "ssh-ed25519 ") {
		t.Fatal(code, m)
	}
	var sealed []byte
	e.srv.Store.DB.QueryRow(`select deploy_key from github_sources`).Scan(&sealed)
	if bytes.Contains(sealed, []byte("PRIVATE KEY")) || len(sealed) == 0 {
		t.Fatal("a chave privada tem de ficar cifrada no banco")
	}
}

func TestGitHubWebhookDeploysFromOrigin(t *testing.T) {
	origin := newOrigin(t)
	e := newEnv(t)
	e.srv.GH = &ghsrc.Service{Store: e.srv.Store, Box: e.srv.Box, Deploy: e.srv.Deploy, Cfg: e.srv.Cfg, Events: e.srv.Events, Log: e.srv.Log,
		Spawn: func(f func()) { f() }, Remap: func(string) string { return origin }}
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})
	e.bareRepo(t, "blog")
	_, m, _ := e.do(t, "PUT", "/api/v1/apps/blog/github", e.token, map[string]string{"url": "https://github.com/acme/site"})
	secret := m["secret"].(string)
	push := `{"ref":"refs/heads/main","repository":{"full_name":"acme/site"}}`

	for _, c := range []struct {
		name, app, event, secret, body string
		code                           int
	}{
		{"sem assinatura", "blog", "push", "", push, 401},
		{"segredo errado", "blog", "push", "errado", push, 401},
		{"app inexistente", "nada", "push", secret, push, 401},
		{"ping", "blog", "ping", secret, `{"zen":"x"}`, 200},
		{"outra branch", "blog", "push", secret, `{"ref":"refs/heads/dev","repository":{"full_name":"acme/site"}}`, 202},
		{"branch apagada", "blog", "push", secret, `{"ref":"refs/heads/main","deleted":true,"repository":{"full_name":"acme/site"}}`, 202},
		{"outro repositório", "blog", "push", secret, `{"ref":"refs/heads/main","repository":{"full_name":"evil/site"}}`, 202},
		{"outro evento", "blog", "issues", secret, `{}`, 202},
	} {
		if code, msg := e.hook(t, c.app, c.event, "d-"+c.name, c.secret, c.body); code != c.code {
			t.Errorf("%s: %d %q (esperado %d)", c.name, code, msg, c.code)
		}
	}
	var n int
	e.srv.Store.DB.QueryRow(`select count(*) from deploys`).Scan(&n)
	if n != 0 {
		t.Fatalf("nenhum desses eventos deveria ter criado deploy; há %d", n)
	}

	if code, msg := e.hook(t, "blog", "push", "d-1", secret, push); code != 202 || msg != "deploy agendado" {
		t.Fatalf("%d %q", code, msg)
	}
	if code, msg := e.hook(t, "blog", "push", "d-1", secret, push); code != 200 || !strings.Contains(msg, "já tratada") {
		t.Fatalf("a mesma entrega não pode disparar de novo: %d %q", code, msg)
	}
	var id, trigger string
	e.srv.Store.DB.QueryRow(`select id, trigger from deploys`).Scan(&id, &trigger)
	if trigger == "" {
		_, g, _ := e.do(t, "GET", "/api/v1/apps/blog/github", e.token, nil)
		t.Fatalf("nenhum deploy foi criado; estado: %v", g)
	}
	if trigger != "github" {
		t.Fatalf("trigger = %q", trigger)
	}
	if d := e.waitDeploy(t, id); d["status"] != "succeeded" {
		t.Fatalf("deploy: %v", d)
	}

	// sync manual pela API
	code, d, _ := e.do(t, "POST", "/api/v1/apps/blog/github/sync", e.token, nil)
	if code != 202 || d["trigger"] != "github" {
		t.Fatal(code, d)
	}
	e.waitDeploy(t, fmt.Sprint(int64(d["id"].(float64))))

	// trocar o segredo invalida o anterior
	_, m, _ = e.do(t, "POST", "/api/v1/apps/blog/github/secret", e.token, nil)
	fresh := m["secret"].(string)
	if fresh == secret {
		t.Fatal("segredo não mudou")
	}
	if code, _ := e.hook(t, "blog", "push", "d-2", secret, push); code != 401 {
		t.Fatalf("segredo antigo ainda vale: %d", code)
	}
	if code, _ := e.hook(t, "blog", "ping", "d-3", fresh, `{}`); code != 200 {
		t.Fatalf("segredo novo recusado: %d", code)
	}

	// desligar
	if code, _, _ := e.do(t, "DELETE", "/api/v1/apps/blog/github", e.token, nil); code != 204 {
		t.Fatal(code)
	}
	if code, _ := e.hook(t, "blog", "ping", "d-4", fresh, `{}`); code != 401 {
		t.Fatalf("depois de desligar: %d", code)
	}
}

func TestGitHubSyncFailureIsReported(t *testing.T) {
	e := newEnv(t)
	e.srv.GH = &ghsrc.Service{Store: e.srv.Store, Box: e.srv.Box, Deploy: e.srv.Deploy, Cfg: e.srv.Cfg, Events: e.srv.Events, Log: e.srv.Log,
		Remap: func(string) string { return filepath.Join(t.TempDir(), "nao-existe.git") }}
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})
	e.bareRepo(t, "blog")
	e.do(t, "PUT", "/api/v1/apps/blog/github", e.token, map[string]string{"url": "https://github.com/acme/site"})
	code, m, _ := e.do(t, "POST", "/api/v1/apps/blog/github/sync", e.token, nil)
	if code != 502 || m["error"].(map[string]any)["code"] != "github_fetch_failed" {
		t.Fatal(code, m)
	}
	_, m, _ = e.do(t, "GET", "/api/v1/apps/blog/github", e.token, nil)
	if m["last_error"] == nil || m["last_error"] == "" {
		t.Fatalf("o último erro deveria aparecer: %v", m)
	}
}
