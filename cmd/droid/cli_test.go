package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"droidpaas/internal/api/apitest"
	"droidpaas/internal/client"
)

type res struct {
	code     int
	out, err string
}

func setup(t *testing.T) *apitest.Env {
	t.Helper()
	e := apitest.New(t)
	t.Setenv("DROID_CONFIG", t.TempDir()+"/droid/config.toml")
	t.Setenv("DROID_URL", e.URL)
	t.Setenv("DROID_TOKEN", e.Token)
	return e
}

func droid(t *testing.T, stdin string, args ...string) res {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errw)
	return res{code, out.String(), errw.String()}
}

func (r res) ok(t *testing.T) res {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.code, r.out, r.err)
	}
	return r
}

func (r res) has(t *testing.T, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(r.out+r.err, w) {
			t.Errorf("saída sem %q:\nstdout: %s\nstderr: %s", w, r.out, r.err)
		}
	}
}

func TestUsageAndExitCodes(t *testing.T) {
	setup(t)
	for _, c := range []struct {
		args []string
		code int
	}{
		{nil, 2}, {[]string{"nada"}, 2}, {[]string{"apps", "create"}, 2}, {[]string{"apps", "rm", "x"}, 2},
		{[]string{"start"}, 2}, {[]string{"env"}, 2}, {[]string{"env", "x", "set", "SEM_IGUAL"}, 2}, {[]string{"tunnel", "set"}, 2},
		{[]string{"tunnel", "set", "--token", "a", "--clear"}, 2}, {[]string{"deploy-logs", "abc"}, 2}, {[]string{"version"}, 0},
	} {
		if r := droid(t, "", c.args...); r.code != c.code {
			t.Errorf("%v: exit %d, esperado %d (%s%s)", c.args, r.code, c.code, r.out, r.err)
		}
	}
	// não logado → 3
	t.Setenv("DROID_URL", "")
	t.Setenv("DROID_TOKEN", "")
	if r := droid(t, "", "apps"); r.code != 3 || !strings.Contains(r.err, "droid login") {
		t.Fatalf("%+v", r)
	}
}

func TestLogin(t *testing.T) {
	e := apitest.New(t)
	cfg := t.TempDir() + "/c/config.toml"
	t.Setenv("DROID_CONFIG", cfg)
	t.Setenv("DROID_URL", "")
	t.Setenv("DROID_TOKEN", "")
	if r := droid(t, "", "login", "--url", e.URL, "--token", "dp_errado"); r.code != 3 {
		t.Fatalf("token errado: %+v", r)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Fatal("login falho não pode gravar credenciais")
	}
	droid(t, "", "login", "--url", e.URL, "--token", e.Token).ok(t).has(t, "autenticado como admin")
	if st, err := os.Stat(cfg); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config: %v %v", err, st)
	}
	creds, err := client.LoadCreds()
	if err != nil || creds.Token != e.Token {
		t.Fatalf("%+v %v", creds, err)
	}
	droid(t, "", "apps").ok(t) // usa o arquivo salvo
}

func TestAppsLifecycle(t *testing.T) {
	setup(t)
	droid(t, "", "apps", "create", "blog", "--runtime", "static").ok(t).has(t, `app "blog" criado`, "git remote add droid ssh://", "git push droid main")
	droid(t, "", "apps", "create", "blog").has(t, "já existe")
	if r := droid(t, "", "apps", "create", "Inválido"); r.code != 1 || !strings.Contains(r.err, "name") {
		t.Fatalf("%+v", r)
	}
	droid(t, "", "apps").ok(t).has(t, "APP", "blog", "stopped", "static")
	var list []map[string]any
	if err := json.Unmarshal([]byte(droid(t, "", "apps", "--json").ok(t).out), &list); err != nil || len(list) != 1 || list[0]["name"] != "blog" {
		t.Fatalf("--json: %v %v", list, err)
	}
	if r := droid(t, "", "start", "blog"); r.code != 1 || !strings.Contains(r.err, "deploy") {
		t.Fatalf("start sem release: %+v", r)
	}
	if r := droid(t, "", "apps", "rm", "blog"); r.code != 2 {
		t.Fatalf("rm sem --yes: %+v", r)
	}
	droid(t, "", "apps", "rm", "blog", "--yes").ok(t).has(t, "removido")
	if r := droid(t, "", "start", "blog"); r.code != 1 || !strings.Contains(r.err, "inexistente") {
		t.Fatalf("%+v", r)
	}
}

func TestEnv(t *testing.T) {
	setup(t)
	droid(t, "", "apps", "create", "web").ok(t)
	droid(t, "", "env", "web", "set", "LOG=info", "URL=a=b=c").ok(t).has(t, "variáveis salvas")
	droid(t, "", "env", "web", "set", "DB=postgres://x", "--secret").ok(t)
	r := droid(t, "", "env", "web").ok(t)
	r.has(t, "LOG=info", "URL=a=b=c", "DB=••••")
	if strings.Contains(r.out, "postgres") {
		t.Fatal("segredo vazou no list")
	}
	droid(t, "", "env", "web", "reveal", "DB").ok(t).has(t, "postgres://x")
	droid(t, "", "env", "web", "unset", "LOG", "--restart").ok(t)
	if strings.Contains(droid(t, "", "env", "web", "list").out, "LOG=") {
		t.Fatal("unset não removeu")
	}
	if r := droid(t, "", "env", "web", "set", "PORT=1"); r.code != 1 || !strings.Contains(r.err, "reservada") {
		t.Fatalf("%+v", r)
	}
}

func TestDomainsKeysTokens(t *testing.T) {
	setup(t)
	droid(t, "", "apps", "create", "web").ok(t)
	droid(t, "", "domains", "web", "add", "web.exemplo.com").ok(t)
	droid(t, "", "domains", "web").ok(t).has(t, "web.exemplo.com")
	droid(t, "", "domains", "web", "rm", "web.exemplo.com").ok(t)

	if _, err := exec.LookPath("ssh-keygen"); err == nil {
		dir := t.TempDir()
		exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "laptop", "-f", dir+"/k").Run()
		droid(t, "", "keys", "add", dir+"/k.pub").ok(t).has(t, "chave #1 adicionada", "SHA256:")
		droid(t, "", "keys", "ls").ok(t).has(t, "laptop")
		pub, _ := os.ReadFile(dir + "/k2.pub")
		_ = pub
		droid(t, "", "keys", "rm", "1").ok(t)
	}
	if r := droid(t, "lixo", "keys", "add", "-"); r.code != 1 {
		t.Fatalf("chave inválida por stdin: %+v", r)
	}

	r := droid(t, "", "tokens", "create", "ci", "--scope", "git").ok(t)
	r.has(t, "dp_")
	droid(t, "", "tokens", "ls").ok(t).has(t, "ci", "git", "ativo")
	droid(t, "", "tokens", "rm", "2").ok(t)
	droid(t, "", "tokens").ok(t).has(t, "revogado")
}

func TestDeployFollowAndLogs(t *testing.T) {
	e := setup(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	droid(t, "", "apps", "create", "blog").ok(t)
	e.PushSite(t, "blog", map[string]string{"index.html": "<h1>oi</h1>"})
	r := droid(t, "", "deploy", "blog").ok(t)
	r.has(t, "-----> fetch", "runtime: static", "-----> finalize", "succeeded")
	droid(t, "", "deploys", "blog").ok(t).has(t, "#1", "succeeded", "manual")
	droid(t, "", "deploy-logs", "1").ok(t).has(t, "-----> fetch")
	droid(t, "", "deploy-logs", "#1", "-f").ok(t).has(t, "succeeded")
	if r := droid(t, "", "deploy", "blog", "--ref", "nao-existe"); r.code != 1 || !strings.Contains(r.err, "ref") {
		t.Fatalf("%+v", r)
	}
	if r := droid(t, "", "rollback", "blog"); r.code != 1 || !strings.Contains(r.err, "anterior") {
		t.Fatalf("rollback sem anterior: %+v", r)
	}
	// --no-follow e --json
	r = droid(t, "", "deploy", "blog", "--no-follow").ok(t)
	r.has(t, "na fila")
	// deploy que falha → exit 1 e o log mostra o motivo
	e.PushSite(t, "blog", map[string]string{"README": "sem runtime"})
	r = droid(t, "", "deploy", "blog")
	if r.code != 1 {
		t.Fatalf("deploy falho deveria sair com 1: %+v", r)
	}
	r.has(t, "detectar o runtime")
	droid(t, "", "logs", "blog", "-n", "5").ok(t) // sem logs de runtime ainda: ok e vazio
}

func TestStatus(t *testing.T) {
	setup(t)
	droid(t, "", "status").ok(t).has(t, "droidd test", "dp-caddy", "sshd", "apps: 0")
	var m map[string]any
	if err := json.Unmarshal([]byte(droid(t, "", "status", "--json").ok(t).out), &m); err != nil || m["version"] != "test" {
		t.Fatalf("%v %v", m, err)
	}
}

func TestParseInterspersed(t *testing.T) {
	c := newCLI(nil, nil, nil)
	fs := c.flags("x")
	f := fs.Bool("f", false, "")
	n := fs.Int("n", 1, "")
	pos, err := parse(fs, []string{"app", "-f", "outro", "-n", "7"})
	if err != nil || !*f || *n != 7 || strings.Join(pos, ",") != "app,outro" {
		t.Fatalf("%v %v %v %v", pos, *f, *n, err)
	}
}
