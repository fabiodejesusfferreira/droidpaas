package gitsrv

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"droidpaas/internal/config"
	"droidpaas/internal/store"
)

func TestParseShellCommand(t *testing.T) {
	good := map[string][2]string{
		"git-receive-pack 'minha-api.git'":  {"git-receive-pack", "minha-api"},
		"git-receive-pack '/minha-api.git'": {"git-receive-pack", "minha-api"},
		"git-upload-pack '/blog.git'":       {"git-upload-pack", "blog"},
	}
	for in, want := range good {
		op, app, err := ParseShellCommand(in)
		if err != nil || op != want[0] || app != want[1] {
			t.Errorf("%q: %q %q %v", in, op, app, err)
		}
	}
	for _, in := range []string{
		"", "ls", "bash", "scp -t /tmp", "git-receive-pack", "git-receive-pack 'app'", "git-receive-pack 'app.git",
		"git-receive-pack '../../etc.git'", "git-receive-pack '/etc/passwd.git'", "git-receive-pack 'a/b.git'",
		"git-receive-pack 'App.git'", "git-receive-pack '1app.git'", "git-receive-pack 'a.git' ; id",
		"git-receive-pack 'app.git'; rm -rf ~", "git-receive-pack '$(id).git'", "git-receive-pack '.git'",
		"git-shell -c 'x'", "git-upload-archive 'app.git'", "git-receive-pack '-x.git'",
	} {
		if _, _, err := ParseShellCommand(in); err == nil {
			t.Errorf("%q deveria ser negado", in)
		}
	}
}

func TestScripts(t *testing.T) {
	h := HookScript("/h/bin/droid", "/h/run/droidd.sock", "api-x")
	want := "#!/bin/sh\n# Gerado pelo DroidPaaS — não edite.\nexec '/h/bin/droid' hook post-receive --app 'api-x' --socket '/h/run/droidd.sock'\n"
	if h != want {
		t.Fatal(h)
	}
	l := AuthorizedLine("/h/bin/droid", "/h", 7, "ssh-ed25519 AAAA nome")
	if l != `command="/h/bin/droid git-shell --home /h --key 7",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA nome` {
		t.Fatal(l)
	}
	if strings.Contains(AuthorizedLine(`/h"x/droid`, "/h", 1, "k"), `"x`) {
		t.Fatal("aspas no caminho não podem escapar do command=")
	}
}

func TestMergeManagedBlock(t *testing.T) {
	mine := []string{"K1", "K2"}
	cases := []struct {
		name, in string
		lines    []string
		want     string
	}{
		{"vazio+chaves", "", mine, "# BEGIN DROIDPAAS\nK1\nK2\n# END DROIDPAAS\n"},
		{"preserva o resto", "ssh-rsa USER\n", mine, "ssh-rsa USER\n# BEGIN DROIDPAAS\nK1\nK2\n# END DROIDPAAS\n"},
		{"substitui", "a\n# BEGIN DROIDPAAS\nVELHA\n# END DROIDPAAS\nb\n", []string{"K1"}, "a\n# BEGIN DROIDPAAS\nK1\n# END DROIDPAAS\nb\n"},
		{"remove com zero chaves", "a\n# BEGIN DROIDPAAS\nVELHA\n# END DROIDPAAS\nb\n", nil, "a\nb\n"},
		{"arquivo só com o bloco", "# BEGIN DROIDPAAS\nX\n# END DROIDPAAS\n", nil, ""},
		{"bloco sem fim", "a\n# BEGIN DROIDPAAS\nVELHA\nb\n", []string{"K1"}, "a\n# BEGIN DROIDPAAS\nK1\n# END DROIDPAAS\n"},
	}
	for _, c := range cases {
		if got := MergeManagedBlock(c.in, c.lines); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if again := MergeManagedBlock(MergeManagedBlock(c.in, c.lines), c.lines); again != MergeManagedBlock(c.in, c.lines) {
			t.Errorf("%s: não idempotente", c.name)
		}
	}
}

func genKey(t *testing.T) string {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, _ := ssh.NewPublicKey(pub)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func TestParsePublicKey(t *testing.T) {
	k := genKey(t)
	canon, fp, comment, err := ParsePublicKey(k + " meu-laptop\n")
	if err != nil || !strings.HasPrefix(canon, "ssh-ed25519 ") || !strings.HasPrefix(fp, "SHA256:") || comment != "meu-laptop" {
		t.Fatalf("%q %q %q %v", canon, fp, comment, err)
	}
	if strings.Contains(canon, "meu-laptop") {
		t.Fatal("a forma canônica não leva comentário")
	}
	for name, in := range map[string]string{
		"vazia": "", "lixo": "isto não é chave", "com opções": `command="rm -rf /" ` + k,
		"duas": k + "\n" + k, "newline embutido": k + "\nssh-rsa AAA", "from=": `from="1.2.3.4" ` + k,
	} {
		if _, _, _, err := ParsePublicKey(in); err == nil {
			t.Errorf("%s deveria falhar", name)
		}
	}
}

func newSvc(t *testing.T) (*Service, config.Config) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	cfg.Git.AuthorizedKeys = filepath.Join(t.TempDir(), ".ssh", "authorized_keys")
	return &Service{Cfg: cfg, Store: st}, cfg
}

func TestInitCreatesBareRepoAndHook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	s, cfg := newSvc(t)
	if err := s.Init(context.Background(), "blog"); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(cfg.Dir("apps", "blog"), "repo.git")
	if out, err := exec.Command("git", "--git-dir", repo, "rev-parse", "--is-bare-repository").Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Fatalf("repo bare: %q %v", out, err)
	}
	hook := filepath.Join(repo, "hooks", "post-receive")
	b, _ := os.ReadFile(hook)
	if !strings.Contains(string(b), "hook post-receive --app 'blog' --socket '"+cfg.SocketPath()+"'") {
		t.Fatalf("hook: %s", b)
	}
	if st, _ := os.Stat(hook); st.Mode()&0o100 == 0 {
		t.Fatal("hook sem permissão de execução")
	}
	if err := s.Init(context.Background(), "blog"); err != nil {
		t.Fatalf("Init deve ser idempotente: %v", err)
	}
}

func TestSyncKeys(t *testing.T) {
	s, cfg := newSvc(t)
	ctx := context.Background()
	os.MkdirAll(filepath.Dir(cfg.Git.AuthorizedKeys), 0o700)
	os.WriteFile(cfg.Git.AuthorizedKeys, []byte("ssh-rsa MINHA-CHAVE-DE-LOGIN\n"), 0o600)
	uid, _ := s.Store.CreateUser(ctx, "admin", "h")
	k1, k2 := genKey(t), genKey(t)
	id1, _ := s.Store.AddSSHKey(ctx, uid, "a", k1, "SHA256:a")
	s.Store.AddSSHKey(ctx, uid, "b", k2, "SHA256:b")
	if err := s.SyncKeys(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg.Git.AuthorizedKeys)
	text := string(got)
	if !strings.HasPrefix(text, "ssh-rsa MINHA-CHAVE-DE-LOGIN\n# BEGIN DROIDPAAS\n") || strings.Count(text, "git-shell --home") != 2 ||
		!strings.Contains(text, k1) || !strings.Contains(text, k2) || !strings.HasSuffix(text, "# END DROIDPAAS\n") {
		t.Fatalf("authorized_keys:\n%s", text)
	}
	if st, _ := os.Stat(cfg.Git.AuthorizedKeys); st.Mode().Perm() != 0o600 {
		t.Fatalf("modo %v", st.Mode())
	}
	s.Store.DeleteSSHKey(ctx, id1)
	s.SyncKeys(ctx)
	got, _ = os.ReadFile(cfg.Git.AuthorizedKeys)
	if strings.Contains(string(got), k1) || !strings.Contains(string(got), k2) || !strings.Contains(string(got), "MINHA-CHAVE-DE-LOGIN") {
		t.Fatalf("remoção:\n%s", got)
	}
	// sem desabilitar: desligar o SSH não mexe no arquivo
	s.Cfg.Git.SSHEnabled = false
	before, _ := os.ReadFile(cfg.Git.AuthorizedKeys)
	s.SyncKeys(ctx)
	if after, _ := os.ReadFile(cfg.Git.AuthorizedKeys); string(after) != string(before) {
		t.Fatal("ssh_enabled=false não deve alterar o arquivo")
	}
}

func TestReadPushLines(t *testing.T) {
	ups := ReadPushLines(bufio.NewScanner(strings.NewReader("aaa bbb refs/heads/main\nlixo\nccc ddd refs/tags/v1\n")))
	if len(ups) != 2 || ups[0].New != "bbb" || ups[1].Ref != "refs/tags/v1" {
		t.Fatalf("%+v", ups)
	}
}
