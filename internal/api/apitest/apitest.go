// Package apitest sobe um droidd em memória (API + deploy engine com runit fake) para testes de CLI e de integração.
package apitest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"droidpaas/internal/api"
	"droidpaas/internal/apps"
	"droidpaas/internal/auth"
	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/deploy"
	"droidpaas/internal/events"
	"droidpaas/internal/gitsrv"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
	"droidpaas/internal/system"
)

type Env struct {
	URL, Token string
	Srv        *api.Server
	Runit      *runit.Fake
}

func New(t *testing.T) *Env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypt.New(bytes.Repeat([]byte{1}, 32))
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	cfg.Ports.RangeStart, cfg.Ports.RangeEnd = 24000, 24050
	cfg.Git.AuthorizedKeys = filepath.Join(t.TempDir(), "authorized_keys")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := &events.Log{DB: st.DB, Log: log}
	rt := runit.NewFake()
	gs := &gitsrv.Service{Store: st, Cfg: cfg}
	svc := &apps.Service{Store: st, Runit: rt, Box: box, Events: ev, Cfg: cfg, Prefix: "/p", Git: gs, PortFree: func(int) bool { return true }}
	eng := deploy.New(&deploy.Engine{Store: st, Apps: svc, Runit: rt, Git: system.GitCLI{}, Runner: system.ExecRunner{}, Cfg: cfg, Events: ev, Log: log,
		Health: func(context.Context, int, string) error { return nil }})
	svc.Deploying = eng.IsDeploying
	go eng.Run(ctx)
	srv := &api.Server{Store: st, Events: ev, Box: box, Cfg: cfg, Version: "test", Log: log, Apps: svc, Deploy: eng, Git: gs, Runit: rt}
	uid, _ := st.CreateUser(ctx, "admin", "hash")
	tok, h, p, _ := auth.NewToken()
	if _, err := st.CreateToken(ctx, uid, "t", h, p, "full"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Public())
	t.Cleanup(ts.Close)
	return &Env{URL: ts.URL, Token: tok, Srv: srv, Runit: rt}
}

// PushSite grava um commit com os arquivos no repo bare do app (que o Create já inicializou).
func (e *Env) PushSite(t *testing.T, app string, files map[string]string) {
	t.Helper()
	repo := filepath.Join(e.Srv.Apps.AppDir(app), "repo.git")
	work := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = work
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for n, b := range files {
		p := filepath.Join(work, n)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(b), 0o755)
	}
	run("add", "-A")
	run("commit", "-qm", "site")
	run("push", "-q", "-f", repo, "main")
}
