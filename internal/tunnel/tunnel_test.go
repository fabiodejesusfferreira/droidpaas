package tunnel

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

func newCtl(t *testing.T) (*Controller, *runit.Fake) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypt.New(bytes.Repeat([]byte{2}, 32))
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	rt := runit.NewFake()
	return &Controller{Store: st, Cfg: cfg, Runit: rt, Box: box, Events: &events.Log{DB: st.DB, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}, rt
}

func setToken(t *testing.T, c *Controller, tok string) {
	t.Helper()
	sealed, _ := c.Box.Seal([]byte(tok))
	c.Store.SetSetting(context.Background(), "tunnel_token", string(sealed))
}

func TestSpecNeverContainsToken(t *testing.T) {
	cfg := config.Default()
	cfg.SetHome("/h")
	s := Spec(cfg)
	if s.Name != "dp-cloudflared" || !strings.Contains(s.Run, "chpst -e /h/etc/tunnel-env cloudflared tunnel --no-autoupdate run") {
		t.Fatalf("%+v", s)
	}
	if strings.Contains(s.Run, "--token") || strings.Contains(s.Run, "TUNNEL_TOKEN=") {
		t.Fatal("o token não pode aparecer no script (nem no ps)")
	}
}

func TestStepLifecycle(t *testing.T) {
	c, rt := newCtl(t)
	ctx := context.Background()
	if err := c.Step(ctx); err != nil || len(rt.Svcs) != 0 {
		t.Fatalf("sem token nada deve ser criado: %v %v", err, rt.Svcs)
	}
	if st := State(ctx, c.Cfg, c.Store, rt); st != "not_configured" {
		t.Fatal(st)
	}

	setToken(t, c, "TOKEN-1")
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	s := rt.Svcs["dp-cloudflared"]
	if s == nil || !s.Up {
		t.Fatalf("serviço não criado/ligado: %+v", s)
	}
	f := filepath.Join(c.Cfg.Dir("etc", "tunnel-env"), "TUNNEL_TOKEN")
	if b, _ := os.ReadFile(f); string(b) != "TOKEN-1" {
		t.Fatalf("arquivo do token: %q", b)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o600 {
		t.Fatalf("modo %v", st.Mode())
	}
	if st, _ := os.Stat(filepath.Dir(f)); st.Mode().Perm() != 0o700 {
		t.Fatalf("modo do diretório %v", st.Mode())
	}
	starts := s.Starts
	c.Step(ctx)
	c.Step(ctx)
	if s.Starts != starts {
		t.Fatal("ciclos sem mudança não podem reiniciar o túnel")
	}

	setToken(t, c, "TOKEN-2")
	c.Step(ctx)
	if b, _ := os.ReadFile(f); string(b) != "TOKEN-2" || s.Starts != starts+1 {
		t.Fatalf("troca de token deve atualizar o arquivo e reiniciar: %q starts=%d", b, s.Starts)
	}

	// serviço parado por fora volta
	s.Up = false
	c.Step(ctx)
	if !s.Up {
		t.Fatal("deveria religar")
	}

	// limpar o token remove tudo
	c.Store.DeleteSetting(ctx, "tunnel_token")
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Svcs["dp-cloudflared"]; ok {
		t.Fatal("serviço deveria ter sido removido")
	}
	if _, err := os.Stat(f); err == nil {
		t.Fatal("arquivo do token deveria ter sido apagado")
	}
}

func TestState(t *testing.T) {
	c, rt := newCtl(t)
	ctx := context.Background()
	setToken(t, c, "T")
	if st := State(ctx, c.Cfg, c.Store, rt); st != "down" {
		t.Fatal(st)
	}
	c.Step(ctx)
	if st := State(ctx, c.Cfg, c.Store, rt); st != "connecting" {
		t.Fatalf("sem log ainda: %s", st)
	}
	ld := c.Cfg.Dir("logs", "cloudflared")
	os.MkdirAll(ld, 0o700)
	write := func(s string) { os.WriteFile(filepath.Join(ld, "current"), []byte(s), 0o644) }
	write("2026-10-10_01:00:00.00000 INF Registered tunnel connection connIndex=0\n")
	if st := State(ctx, c.Cfg, c.Store, rt); st != "connected" {
		t.Fatal(st)
	}
	write("2026-10-10_01:00:00.00000 INF Registered tunnel connection connIndex=0\n2026-10-10_01:00:09.00000 ERR Unregistered tunnel connection\n")
	if st := State(ctx, c.Cfg, c.Store, rt); st != "connecting" {
		t.Fatal(st)
	}
	rt.Svcs["dp-cloudflared"].Up = false
	if st := State(ctx, c.Cfg, c.Store, rt); st != "down" {
		t.Fatal(st)
	}
}
