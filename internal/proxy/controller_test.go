package proxy

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

func TestControllerStep(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	rt := runit.NewFake()
	fc := &fakeClient{}
	ev := &events.Log{DB: st.DB, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c := NewController(st, cfg, rt, fc, ev)

	step := func() {
		t.Helper()
		if err := c.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	step() // cria o dp-caddy e o liga (serviço novo nasce up no Fake; Status → run)
	if _, ok := rt.Svcs["dp-caddy"]; !ok {
		t.Fatal("dp-caddy não criado")
	}
	if fc.loads != 1 {
		t.Fatalf("primeira aplicação: loads=%d", fc.loads)
	}
	if _, err := filepath.Glob(cfg.Dir("proxy", "caddy.json")); err != nil {
		t.Fatal(err)
	}
	step()
	if fc.loads != 1 {
		t.Fatalf("sem mudança não recarrega: loads=%d", fc.loads)
	}
	// mudança de estado desejado → nova config
	a := store.App{Name: "x", Runtime: "auto", Branch: "main", HealthTimeoutS: 30, DesiredState: "running"}
	p := 20001
	a.Port = &p
	st.InsertApp(ctx, &a)
	st.AddDomain(ctx, a.ID, "x.ex.com", true, true)
	step()
	if fc.loads != 2 {
		t.Fatalf("app novo deveria recarregar: loads=%d", fc.loads)
	}
	// o Caddy reinicia sozinho e volta ao bootstrap → reaplica
	rt.Crash("dp-caddy")
	step()
	if fc.loads != 3 {
		t.Fatalf("restart do Caddy deveria reaplicar: loads=%d", fc.loads)
	}
	// falha do Caddy: erro propagado, evento registrado, config anterior mantida
	fc.fail = true
	st.AddDomain(ctx, a.ID, "y.ex.com", false, false)
	if err := c.Step(ctx); err == nil {
		t.Fatal("falha deveria ser propagada")
	}
	evs, _ := ev.List(ctx, 10, 0)
	found := false
	for _, e := range evs {
		found = found || e.Kind == "proxy.apply_failed"
	}
	if !found {
		t.Fatal("evento proxy.apply_failed ausente")
	}
	// dp-caddy parado de fora → o controlador religa
	fc.fail = false
	rt.Svcs["dp-caddy"].Up = false
	step()
	if !rt.Svcs["dp-caddy"].Up {
		t.Fatal("dp-caddy deveria ter sido religado")
	}
	_ = time.Second
}
