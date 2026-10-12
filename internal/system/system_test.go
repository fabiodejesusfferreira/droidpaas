package system

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkTar(t *testing.T, entries ...tar.Header) *bytes.Reader {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, h := range entries {
		h := h
		body := ""
		if h.Typeflag == tar.TypeReg {
			body = "x"
			h.Size = 1
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	return bytes.NewReader(b.Bytes())
}

func TestExtractTarOK(t *testing.T) {
	d := filepath.Join(t.TempDir(), "rel")
	err := ExtractTar(mkTar(t,
		tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader},
		tar.Header{Name: "src/", Typeflag: tar.TypeDir, Mode: 0o755},
		tar.Header{Name: "src/a.js", Typeflag: tar.TypeReg, Mode: 0o644},
		tar.Header{Name: "run.sh", Typeflag: tar.TypeReg, Mode: 0o755},
		tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "src/a.js"},
	), d)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(d, "run.sh")); err != nil || st.Mode()&0o100 == 0 {
		t.Fatalf("bit de execução perdido: %v %v", err, st)
	}
	if got, _ := os.Readlink(filepath.Join(d, "link")); got != "src/a.js" {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(d, "src", "a.js")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarRejectsEscapes(t *testing.T) {
	for name, entries := range map[string][]tar.Header{
		"..":       {{Name: "../fora", Typeflag: tar.TypeReg, Mode: 0o644}},
		"meio":     {{Name: "a/../../fora", Typeflag: tar.TypeReg, Mode: 0o644}},
		"absoluto": {{Name: "/tmp/fora", Typeflag: tar.TypeReg, Mode: 0o644}},
		"hardlink": {{Name: "h", Typeflag: tar.TypeLink, Linkname: "/etc/passwd"}},
		"via-symlink": {
			{Name: "s", Typeflag: tar.TypeSymlink, Linkname: "/tmp"},
			{Name: "s/pwn", Typeflag: tar.TypeReg, Mode: 0o644},
		},
	} {
		root := t.TempDir()
		d := filepath.Join(root, "rel")
		if err := ExtractTar(mkTar(t, entries...), d); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
		if _, err := os.Stat(filepath.Join(root, "fora")); err == nil {
			t.Errorf("%s: escreveu fora do destino", name)
		}
	}
	if _, err := os.Stat("/tmp/pwn"); err == nil {
		os.Remove("/tmp/pwn")
		t.Fatal("symlink permitiu escrever em /tmp")
	}
}

func gitRepo(t *testing.T) (work, bare string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	root := t.TempDir()
	work, bare = filepath.Join(root, "w"), filepath.Join(root, "r.git")
	run := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(work, 0o755)
	run(work, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(work, "app.js"), []byte("console.log(1)"), 0o644)
	os.WriteFile(filepath.Join(work, "run.sh"), []byte("#!/bin/sh"), 0o755)
	run(work, "add", ".")
	run(work, "commit", "-qm", "primeiro commit\n\ncorpo")
	run(root, "clone", "-q", "--bare", work, bare)
	return
}

func TestGitCLI(t *testing.T) {
	_, bare := gitRepo(t)
	g := GitCLI{}
	ctx := context.Background()
	sha, err := g.RevParse(ctx, bare, "main")
	if err != nil || len(sha) != 40 {
		t.Fatalf("%q %v", sha, err)
	}
	for _, bad := range []string{"--output=/tmp/x", "-h", "nao-existe", "a..b", ""} {
		if _, err := g.RevParse(ctx, bare, bad); err == nil {
			t.Errorf("ref %q deveria falhar", bad)
		}
	}
	if s, err := g.Subject(ctx, bare, sha); err != nil || s != "primeiro commit" {
		t.Fatalf("%q %v", s, err)
	}
	dest := filepath.Join(t.TempDir(), "rel")
	if err := g.Archive(ctx, bare, sha, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "app.js")); string(b) != "console.log(1)" {
		t.Fatal("conteúdo")
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		t.Fatal("release não deve ter .git")
	}
	if st, _ := os.Stat(filepath.Join(dest, "run.sh")); st.Mode()&0o100 == 0 {
		t.Fatal("bit de execução")
	}
	if err := g.Archive(ctx, bare, "--exec=x", t.TempDir()); err == nil {
		t.Fatal("sha inválido deveria falhar")
	}
}

func TestExecRunner(t *testing.T) {
	var out bytes.Buffer
	r := ExecRunner{}
	ctx := context.Background()
	if err := r.Run(ctx, "", os.Environ(), &out, "sh", "-c", "echo oi; echo erro >&2"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "oi") || !strings.Contains(out.String(), "erro") {
		t.Fatalf("stdout+stderr: %q", out.String())
	}
	if err := r.Run(ctx, "", os.Environ(), &out, "sh", "-c", "exit 3"); err == nil {
		t.Fatal("exit 3 deveria falhar")
	}
	var o2 bytes.Buffer
	r.Run(ctx, t.TempDir(), []string{"FOO=bar"}, &o2, "sh", "-c", "echo $FOO; pwd")
	if !strings.HasPrefix(o2.String(), "bar\n") {
		t.Fatalf("env/dir: %q", o2.String())
	}
}

func TestExecRunnerKillsWholeGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "vivo")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// o neto ignora SIGTERM e continuaria escrevendo se só o pai fosse morto
	script := "(trap '' TERM; while :; do echo x > " + marker + "; sleep 0.1; done) & wait"
	start := time.Now()
	err := ExecRunner{KillAfter: 300 * time.Millisecond}.Run(ctx, "", os.Environ(), &bytes.Buffer{}, "sh", "-c", script)
	if err == nil {
		t.Fatal("cancelamento deveria devolver erro")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("demorou %v", time.Since(start))
	}
	os.Remove(marker)
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("processo neto sobreviveu ao cancelamento")
	}
}
