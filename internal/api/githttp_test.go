package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), append([]string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_TERMINAL_PROMPT=0"}, env...)...)
	out, err := c.CombinedOutput()
	return string(out), err
}

func TestGitHTTP(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	if out, _ := exec.Command("git", "--exec-path").Output(); len(out) > 0 {
		if _, err := os.Stat(filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")); err != nil {
			t.Skip("git-http-backend indisponível")
		}
	}
	e := newEnv(t)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})
	e.gitSite(t, "blog")
	host := strings.TrimPrefix(e.ts.URL, "http://")
	withTok := func(tok string) string { return "http://u:" + tok + "@" + host + "/git/blog.git" }

	// sem credencial → 401 com desafio Basic
	resp, _ := http.Get(e.ts.URL + "/git/blog.git/info/refs?service=git-upload-pack")
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	if out, err := runGit(t, t.TempDir(), nil, "clone", "-q", withTok("dp_invalido"), "x"); err == nil {
		t.Fatalf("token inválido não pode clonar:\n%s", out)
	}
	// repositório/app inexistente → 404 (com credencial válida)
	_, m, _ := e.do(t, "POST", "/api/v1/tokens", e.token, map[string]string{"name": "ci", "scope": "git"})
	gitTok, gitID := m["token"].(string), itoa(int64(m["id"].(float64)))
	resp, _ = http.DefaultClient.Do(func() *http.Request {
		r, _ := http.NewRequest("GET", e.ts.URL+"/git/nada.git/info/refs?service=git-upload-pack", nil)
		r.SetBasicAuth("u", gitTok)
		return r
	}())
	if resp.StatusCode != 404 {
		t.Fatalf("app inexistente: %d", resp.StatusCode)
	}

	// clone + push com token de escopo git
	work := t.TempDir()
	if out, err := runGit(t, work, nil, "clone", "-q", withTok(gitTok), "w"); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	w := filepath.Join(work, "w")
	if _, err := os.Stat(filepath.Join(w, "index.html")); err != nil {
		t.Fatal("clone sem o conteúdo")
	}
	os.WriteFile(filepath.Join(w, "novo.txt"), []byte("via http"), 0o644)
	runGit(t, w, nil, "add", ".")
	runGit(t, w, nil, "commit", "-qm", "via http")
	if out, err := runGit(t, w, nil, "push", "-q", "origin", "HEAD:main"); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	repo := filepath.Join(e.srv.Apps.AppDir("blog"), "repo.git")
	if out, _ := runGit(t, repo, nil, "--git-dir", repo, "log", "--format=%s", "-1", "main"); strings.TrimSpace(out) != "via http" {
		t.Fatalf("o push não chegou ao repo: %q", out)
	}

	// token do escopo full também serve; revogado deixa de servir
	if out, err := runGit(t, t.TempDir(), nil, "ls-remote", withTok(e.token)); err != nil {
		t.Fatalf("token full: %v\n%s", err, out)
	}
	e.do(t, "DELETE", "/api/v1/tokens/"+gitID, e.token, nil)
	if _, err := runGit(t, t.TempDir(), nil, "ls-remote", withTok(gitTok)); err == nil {
		t.Fatal("token revogado não pode acessar")
	}
	// desligado nas configurações → 404
	e.do(t, "PUT", "/api/v1/settings", e.token, map[string]string{"git_http_enabled": "false"})
	if _, err := runGit(t, t.TempDir(), nil, "ls-remote", withTok(e.token)); err == nil {
		t.Fatal("Git HTTP desligado não pode responder")
	}
}

func TestGitHTTPRejectsBadPaths(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/git/", "/git/..%2f..%2fetc.git/info/refs", "/git/Blog.git/info/refs", "/git/a.git/../../x", "/git/blog/info/refs"} {
		req, _ := http.NewRequest("GET", e.ts.URL+p, nil)
		req.SetBasicAuth("u", e.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 404 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestBackup(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(e.srv.Cfg.KeyPath(), bytes.Repeat([]byte{1}, 32), 0o600)
	os.MkdirAll(filepath.Dir(e.srv.Cfg.KeyPath()), 0o700)
	os.WriteFile(e.srv.Cfg.KeyPath(), bytes.Repeat([]byte{1}, 32), 0o600)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})

	get := func(q string) (int, []byte, http.Header) {
		req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/backup"+q, nil)
		req.Header.Set("Authorization", "Bearer "+e.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header
	}
	code, b, h := get("")
	if code != 200 || !bytes.HasPrefix(b, []byte("SQLite format 3\x00")) || !strings.Contains(h.Get("Content-Disposition"), ".db") {
		t.Fatalf("%d %q", code, b[:min(len(b), 20)])
	}
	if bytes.Contains(b, bytes.Repeat([]byte{1}, 32)) {
		t.Fatal("o backup simples não pode conter a master.key")
	}
	if !bytes.Contains(b, []byte("blog")) {
		t.Fatal("o backup deve conter os dados")
	}

	code, b, h = get("?key=1")
	if code != 200 || h.Get("Content-Type") != "application/gzip" {
		t.Fatalf("%d %v", code, h)
	}
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]int{}
	for {
		hd, err := tr.Next()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(tr)
		names[hd.Name] = len(body)
	}
	if names["master.key"] != 32 || names["droidpaas.db"] < 1000 {
		t.Fatalf("conteúdo do tar: %v", names)
	}
	if left, _ := filepath.Glob(filepath.Join(e.srv.Cfg.Dir("tmp"), "backup-*")); len(left) != 0 {
		t.Fatalf("arquivos temporários sobraram: %v", left)
	}
	if _, _, raw := e.do(t, "GET", "/api/v1/events", e.token, nil); !strings.Contains(string(raw), "backup.created") {
		t.Fatal("backup deve ser auditado")
	}
}
