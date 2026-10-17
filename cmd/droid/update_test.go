package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeEnv simula git/go/sv e uma pasta de binários real; calls guarda os comandos na ordem.
type fakeEnv struct {
	t                  *testing.T
	repo               bool
	dirty, head, want  string
	buildFails         bool
	healthyAfterSwap   bool
	calls              []string
	swapped, restarted int
}

func (f *fakeEnv) sh(dir string, env []string, name string, a ...string) (string, error) {
	cmd := name + " " + strings.Join(a, " ")
	f.calls = append(f.calls, cmd)
	switch {
	case strings.HasPrefix(cmd, "git rev-parse --is-inside-work-tree"):
		if f.repo {
			return "true", nil
		}
		return "fatal: not a git repository", errors.New("x")
	case strings.HasPrefix(cmd, "git status"):
		return f.dirty, nil
	case strings.HasPrefix(cmd, "git rev-parse --verify"):
		return f.want, nil
	case strings.HasPrefix(cmd, "git rev-parse HEAD"):
		return f.head, nil
	case strings.HasPrefix(cmd, "git symbolic-ref"):
		return "main", nil
	case strings.HasPrefix(cmd, "git describe"):
		return "v0.2.0", nil
	case strings.HasPrefix(cmd, "git log"):
		return "abc1234 muda o painel", nil
	case name == "go":
		if f.buildFails {
			return "erro de compilação", errors.New("x")
		}
		out := a[len(a)-2] // ... -o DIR/ ./cmd/...
		for _, n := range []string{"droidd", "droid"} {
			os.WriteFile(filepath.Join(out, n), []byte("new"), 0o755)
		}
		return "", nil
	case strings.HasSuffix(name, "version"), strings.HasSuffix(cmd, " version"):
		return "v0.2.0", nil
	case name == "sv":
		f.restarted++
		return "ok", nil
	}
	return "", nil
}

func (f *fakeEnv) updater(t *testing.T) (*updater, *bytes.Buffer, string) {
	bin := t.TempDir()
	for _, n := range []string{"droidd", "droid"} {
		os.WriteFile(filepath.Join(bin, n), []byte("old"), 0o755)
	}
	var out bytes.Buffer
	u := &updater{out: &out, errw: &out, src: t.TempDir(), bin: bin, sh: f.sh, wait: 20 * time.Millisecond, poll: time.Millisecond}
	u.healthy = func() bool { return f.healthyAfterSwap && f.restarted > 0 || f.restarted > 1 }
	return u, &out, bin
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateNotAClone(t *testing.T) {
	f := &fakeEnv{t: t}
	u, _, _ := f.updater(t)
	err := u.run(false, "", false)
	if err == nil || !strings.Contains(err.Error(), "não é um clone") || !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("erro = %v", err)
	}
}

func TestUpdateRefusesDirtyTree(t *testing.T) {
	f := &fakeEnv{t: t, repo: true, dirty: " M x.go", head: "aaaaaaa1", want: "bbbbbbb2"}
	u, _, _ := f.updater(t)
	if err := u.run(false, "", false); err == nil || !strings.Contains(err.Error(), "mudanças locais") {
		t.Fatalf("erro = %v", err)
	}
}

func TestUpdateAlreadyCurrentAndCheck(t *testing.T) {
	f := &fakeEnv{t: t, repo: true, head: "aaaaaaa1", want: "aaaaaaa1"}
	u, out, _ := f.updater(t)
	if err := u.run(false, "", false); err != nil || !strings.Contains(out.String(), "já está atualizado") {
		t.Fatalf("%v %s", err, out)
	}
	f = &fakeEnv{t: t, repo: true, head: "aaaaaaa1", want: "bbbbbbb2"}
	u, out, bin := f.updater(t)
	if err := u.run(true, "", false); err != nil || !strings.Contains(out.String(), "atualização disponível") {
		t.Fatalf("%v %s", err, out)
	}
	if read(t, filepath.Join(bin, "droidd")) != "old" || f.restarted != 0 {
		t.Fatal("--check não pode trocar binários nem reiniciar")
	}
}

func TestUpdateHappyPath(t *testing.T) {
	f := &fakeEnv{t: t, repo: true, head: "aaaaaaa1", want: "bbbbbbb2", healthyAfterSwap: true}
	u, out, bin := f.updater(t)
	if err := u.run(false, "", false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if read(t, filepath.Join(bin, "droidd")) != "new" || read(t, filepath.Join(bin, "droidd.prev")) != "old" {
		t.Fatal("binários não foram trocados (ou o anterior não foi guardado)")
	}
	if f.restarted != 1 || !strings.Contains(out.String(), "atualizado: aaaaaaa → bbbbbbb") {
		t.Fatalf("restarted=%d\n%s", f.restarted, out)
	}
	if _, err := os.Stat(filepath.Join(bin, ".update-new")); err == nil {
		t.Fatal("pasta temporária ficou para trás")
	}
}

func TestUpdateBuildFailureChangesNothing(t *testing.T) {
	f := &fakeEnv{t: t, repo: true, head: "aaaaaaa1", want: "bbbbbbb2", buildFails: true}
	u, _, bin := f.updater(t)
	err := u.run(false, "", false)
	if err == nil || !strings.Contains(err.Error(), "compilação falhou") {
		t.Fatalf("erro = %v", err)
	}
	if read(t, filepath.Join(bin, "droidd")) != "old" || f.restarted != 0 {
		t.Fatal("falha de build não pode tocar nos binários nem reiniciar")
	}
	if last := f.calls[len(f.calls)-1]; !strings.HasPrefix(last, "git reset --hard") {
		t.Fatalf("o código deveria voltar ao commit anterior; último comando: %q", last)
	}
}

func TestUpdateRollsBackWhenPanelDoesNotReturn(t *testing.T) {
	f := &fakeEnv{t: t, repo: true, head: "aaaaaaa1", want: "bbbbbbb2"} // healthy só depois do 2º restart
	u, out, bin := f.updater(t)
	err := u.run(false, "", false)
	if err == nil || !strings.Contains(err.Error(), "voltei à anterior") {
		t.Fatalf("erro = %v\n%s", err, out)
	}
	if read(t, filepath.Join(bin, "droidd")) != "old" || read(t, filepath.Join(bin, "droid")) != "old" {
		t.Fatal("binários antigos não foram restaurados")
	}
	if f.restarted != 2 {
		t.Fatalf("esperava 2 reinícios (novo e depois o antigo), houve %d", f.restarted)
	}
}

// Com git de verdade: --check enxerga o commit novo no "GitHub" e uma compilação que falha devolve o clone ao commit de antes.
func TestUpdateWithRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	for _, k := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+k+"_NAME", "t")
		t.Setenv("GIT_"+k+"_EMAIL", "t@t")
	}
	root := t.TempDir()
	g := func(dir string, a ...string) string {
		out, err := realSh(dir, nil, "git", a...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
		return out
	}
	origin, src, other := filepath.Join(root, "origin.git"), filepath.Join(root, "src"), filepath.Join(root, "other")
	g(root, "init", "-q", "--bare", "-b", "main", origin)
	g(root, "clone", "-q", origin, src)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	g(src, "add", "-A")
	g(src, "commit", "-qm", "a")
	g(src, "push", "-q", "origin", "HEAD:main")
	g(src, "branch", "-q", "--set-upstream-to=origin/main", "main")
	g(root, "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, "b.txt"), []byte("b"), 0o644)
	g(other, "add", "-A")
	g(other, "commit", "-qm", "muda o painel")
	g(other, "push", "-q", "origin", "HEAD:main")

	bin := t.TempDir()
	for _, n := range []string{"droidd", "droid"} {
		os.WriteFile(filepath.Join(bin, n), []byte("old"), 0o755)
	}
	var out bytes.Buffer
	u := &updater{out: &out, errw: &out, src: src, bin: bin, sh: realSh, wait: time.Millisecond, poll: time.Millisecond, healthy: func() bool { return true }}
	before := g(src, "rev-parse", "HEAD")

	if err := u.run(true, "", false); err != nil || !strings.Contains(out.String(), "atualização disponível") || !strings.Contains(out.String(), "muda o painel") {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if err := u.run(false, "", false); err == nil || !strings.Contains(err.Error(), "compilação falhou") {
		t.Fatalf("o clone de teste não tem go.mod: a compilação deveria falhar; erro = %v", err)
	}
	if after := g(src, "rev-parse", "HEAD"); after != before {
		t.Fatalf("o clone deveria voltar a %s, está em %s", before, after)
	}
	if read(t, filepath.Join(bin, "droidd")) != "old" {
		t.Fatal("binário trocado apesar da falha")
	}
	if err := u.run(false, "nao-existe", false); err == nil || !strings.Contains(err.Error(), "não encontrada") {
		t.Fatalf("erro = %v", err)
	}
}
