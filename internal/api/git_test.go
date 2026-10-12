package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"droidpaas/internal/client"
	"droidpaas/internal/gitsrv"
)

func pubKey(t *testing.T) string {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, _ := ssh.NewPublicKey(pub)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func TestSSHKeysAPI(t *testing.T) {
	e := newEnv(t)
	e.srv.Git = &gitsrv.Service{Store: e.srv.Store, Cfg: e.srv.Cfg}
	e.srv.Cfg.Git.AuthorizedKeys = filepath.Join(t.TempDir(), "authorized_keys")
	e.srv.Git.Cfg = e.srv.Cfg
	k := pubKey(t)
	code, m, _ := e.do(t, "POST", "/api/v1/ssh-keys", e.token, map[string]string{"name": "laptop", "public_key": k + " ignorado"})
	if code != 201 || !strings.HasPrefix(m["fingerprint"].(string), "SHA256:") || m["name"] != "laptop" {
		t.Fatal(code, m)
	}
	if b, _ := os.ReadFile(e.srv.Cfg.Git.AuthorizedKeys); !strings.Contains(string(b), "git-shell --home") || !strings.Contains(string(b), k) {
		t.Fatalf("authorized_keys não atualizado: %s", b)
	}
	if code, _, _ := e.do(t, "POST", "/api/v1/ssh-keys", e.token, map[string]string{"name": "dup", "public_key": k}); code != 409 {
		t.Fatalf("duplicada: %d", code)
	}
	for _, bad := range []string{"lixo", `command="id" ` + k, ""} {
		if code, _, _ := e.do(t, "POST", "/api/v1/ssh-keys", e.token, map[string]string{"name": "x", "public_key": bad}); code != 422 {
			t.Errorf("%q: %d", bad, code)
		}
	}
	// nome vem do comentário quando omitido
	k2 := pubKey(t)
	if code, m, _ := e.do(t, "POST", "/api/v1/ssh-keys", e.token, map[string]string{"public_key": k2 + " do-comentario"}); code != 201 || m["name"] != "do-comentario" {
		t.Fatal(code, m)
	}
	id := itoa(int64(m["id"].(float64)))
	if code, _, _ := e.do(t, "DELETE", "/api/v1/ssh-keys/"+id, e.token, nil); code != 204 {
		t.Fatal(code)
	}
	if b, _ := os.ReadFile(e.srv.Cfg.Git.AuthorizedKeys); strings.Contains(string(b), k) {
		t.Fatal("chave removida ainda está no authorized_keys")
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/ssh-keys/"+id, e.token, nil); code != 404 {
		t.Fatal(code)
	}
}

// unixServer expõe srv.Internal() num socket Unix, como o droidd faz.
func (e *env) unixServer(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: e.srv.Internal()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func TestHookPostReceive(t *testing.T) {
	e := newEnv(t)
	sock := e.unixServer(t)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})
	e.gitSite(t, "blog")
	repo := filepath.Join(e.srv.Apps.AppDir("blog"), "repo.git")
	sha, _ := exec.Command("git", "--git-dir", repo, "rev-parse", "main").Output()
	head := strings.TrimSpace(string(sha))

	// push no branch de deploy: o log do deploy chega em streaming e o código de saída é 0
	var out bytes.Buffer
	code, err := client.PostReceive(context.Background(), sock, "blog", []gitsrv.Update{{Old: gitsrv.ZeroSHA, New: head, Ref: "refs/heads/main"}}, &out)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v\n%s", code, err, out.String())
	}
	for _, want := range []string{"deploy #1 na fila", "-----> fetch", "-----> finalize", "succeeded"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("saída sem %q:\n%s", want, out.String())
		}
	}
	_, m, _ := e.do(t, "GET", "/api/v1/deploys/1", e.token, nil)
	if m["trigger"] != "git_push" || m["git_sha"] != head {
		t.Fatalf("%v", m)
	}

	// outro branch / tag / exclusão de branch: ignorado, sem deploy, código 0
	out.Reset()
	code, err = client.PostReceive(context.Background(), sock, "blog", []gitsrv.Update{
		{Old: gitsrv.ZeroSHA, New: head, Ref: "refs/heads/feature"},
		{Old: head, New: gitsrv.ZeroSHA, Ref: "refs/heads/main"},
		{Old: gitsrv.ZeroSHA, New: head, Ref: "refs/tags/v1"},
	}, &out)
	if err != nil || code != 0 || !strings.Contains(out.String(), "só o branch") {
		t.Fatalf("%d %v %s", code, err, out.String())
	}
	if _, m, _ := e.do(t, "GET", "/api/v1/deploys/2", e.token, nil); m["error"] == nil {
		t.Fatal("não deveria existir o deploy #2")
	}

	// app inexistente → erro
	if _, err := client.PostReceive(context.Background(), sock, "nada", nil, &out); err == nil {
		t.Fatal("app inexistente deveria falhar")
	}
}

func TestHookReportsFailureWithExitCode(t *testing.T) {
	e := newEnv(t)
	sock := e.unixServer(t)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})
	// repositório com um site que não consegue ser detectado: README só
	repo := filepath.Join(e.srv.Apps.AppDir("blog"), "repo.git")
	w := t.TempDir()
	run := func(dir string, args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(w, "init", "-q", "--bare", "-b", "main", repo)
	w2 := t.TempDir()
	run(w2, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(w2, "README"), []byte("x"), 0o644)
	run(w2, "add", ".")
	run(w2, "commit", "-qm", "x")
	run(w2, "push", "-q", repo, "main")
	head := run(w2, "rev-parse", "HEAD")

	var out bytes.Buffer
	code, err := client.PostReceive(context.Background(), sock, "blog", []gitsrv.Update{{Old: gitsrv.ZeroSHA, New: head, Ref: "refs/heads/main"}}, &out)
	if err != nil || code != 1 || !strings.Contains(out.String(), "detectar o runtime") {
		t.Fatalf("code=%d err=%v\n%s", code, err, out.String())
	}
}
