package reconcile

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

func run(pid int) runit.ServiceStatus {
	return runit.ServiceStatus{State: "run", PID: pid, WantsUp: true}
}

func TestCrashTracker(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewCrashTracker(60*time.Second, 5)
	// pid estável nunca é loop
	for i := 0; i < 20; i++ {
		if c.Observe("a", run(10), now.Add(time.Duration(i)*10*time.Second)) {
			t.Fatal("pid estável não é crash")
		}
	}
	// 5 trocas de pid em 50 s → loop na 5ª
	c = NewCrashTracker(60*time.Second, 5)
	var hit int
	for i := 0; i < 8; i++ {
		if c.Observe("b", run(100+i), now.Add(time.Duration(i)*10*time.Second)) {
			hit = i
			break
		}
	}
	if hit != 5 { // a 1ª observação só registra o pid; reinícios contam da 2ª em diante
		t.Fatalf("detectou no ciclo %d, esperado 5", hit)
	}
	// reinícios espalhados (fora da janela) não contam
	c = NewCrashTracker(60*time.Second, 5)
	for i := 0; i < 20; i++ {
		if c.Observe("c", run(200+i), now.Add(time.Duration(i)*30*time.Second)) {
			t.Fatalf("reinício a cada 30 s não é loop com limiar 5/60 s (ciclo %d)", i)
		}
	}
	// serviço parado zera o pid de referência: religar não conta como reinício
	c = NewCrashTracker(60*time.Second, 2)
	c.Observe("d", run(1), now)
	c.Observe("d", runit.ServiceStatus{State: "down"}, now.Add(time.Second))
	c.Observe("d", run(2), now.Add(2*time.Second))
	if c.Observe("d", run(2), now.Add(3*time.Second)) {
		t.Fatal("religar manualmente não é crash")
	}
}

type fx struct {
	step *Apps
	st   *store.Store
	rt   *runit.Fake
	svc  *apps.Service
	now  time.Time
	ctx  context.Context
}

func newFx(t *testing.T) *fx {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypt.New(bytes.Repeat([]byte{9}, 32))
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	cfg.Ports.RangeStart, cfg.Ports.RangeEnd = 23000, 23010
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := &events.Log{DB: st.DB, Log: log}
	rt := runit.NewFake()
	svc := &apps.Service{Store: st, Runit: rt, Box: box, Events: ev, Cfg: cfg, Prefix: "/p", PortFree: func(int) bool { return true }}
	f := &fx{st: st, rt: rt, svc: svc, now: time.Unix(5000, 0), ctx: ctx}
	f.step = &Apps{Store: st, Svc: svc, Runit: rt, Events: ev, Log: log, Tracker: NewCrashTracker(60*time.Second, 5), Now: func() time.Time { return f.now }}
	return f
}

func (f *fx) release(t *testing.T, a store.App, rt string) {
	t.Helper()
	r := store.Release{AppID: a.ID, GitSHA: "a", GitRef: "main", Status: "ready", Path: "/x/1-a", Runtime: rt}
	f.st.InsertRelease(f.ctx, &r)
	f.st.SetCurrentRelease(f.ctx, a.ID, &r.ID)
}

func (f *fx) cycle(t *testing.T) {
	t.Helper()
	if err := f.step.Run(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(10 * time.Second)
}

func TestConvergence(t *testing.T) {
	f := newFx(t)
	a, _ := f.svc.Create(f.ctx, apps.CreateInput{Name: "web"})
	f.cycle(t)
	if f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("app sem release não sobe")
	}
	f.release(t, a, "node")
	f.cycle(t)
	if !f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("desired=running com release deve subir")
	}
	// alguém para o serviço por fora → o reconciliador religa
	f.rt.Svcs["dp-app-web"].Up = false
	f.cycle(t)
	if !f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("deriva não corrigida")
	}
	// desired=stopped → para
	f.st.SetDesiredState(f.ctx, a.ID, "stopped")
	f.cycle(t)
	if f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("desired=stopped deve parar")
	}
	// serviço apagado por fora é recriado
	f.st.SetDesiredState(f.ctx, a.ID, "running")
	delete(f.rt.Svcs, "dp-app-web")
	f.cycle(t)
	if s := f.rt.Svcs["dp-app-web"]; s == nil || !s.Up {
		t.Fatalf("serviço não recriado: %+v", s)
	}
	// idempotência: ciclos extras não reiniciam nada
	starts := f.rt.Svcs["dp-app-web"].Starts
	f.cycle(t)
	f.cycle(t)
	if f.rt.Svcs["dp-app-web"].Starts != starts {
		t.Fatal("ciclos sem mudança não podem reiniciar o app")
	}
}

func TestStaticAppHasNoProcess(t *testing.T) {
	f := newFx(t)
	a, _ := f.svc.Create(f.ctx, apps.CreateInput{Name: "blog"})
	f.release(t, a, "static")
	f.rt.Up(f.ctx, "dp-app-blog") // alguém ligou
	f.cycle(t)
	if f.rt.Svcs["dp-app-blog"].Up {
		t.Fatal("site estático não tem processo")
	}
}

func TestSkipsAppsBeingDeployed(t *testing.T) {
	f := newFx(t)
	a, _ := f.svc.Create(f.ctx, apps.CreateInput{Name: "web"})
	f.release(t, a, "node")
	f.step.Deploying = func(id int64) bool { return id == a.ID }
	f.cycle(t)
	if f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("o reconciliador não pode competir com um deploy em andamento")
	}
}

func TestCrashLoopStopsAndRecordsEvent(t *testing.T) { // AT-09
	f := newFx(t)
	a, _ := f.svc.Create(f.ctx, apps.CreateInput{Name: "web"})
	f.release(t, a, "node")
	logDir := f.svc.LogDir("web")
	os.MkdirAll(logDir, 0o700)
	os.WriteFile(filepath.Join(logDir, "current"), []byte("2026-10-10_01:00:00.00000 boom: cannot find module\n"), 0o644)
	f.cycle(t) // sobe
	for i := 0; i < 8; i++ {
		f.rt.Crash("dp-app-web") // morre e o runit reinicia (pid novo)
		f.cycle(t)
	}
	got, _ := f.svc.Get(f.ctx, "web")
	if got.CrashedAt == nil {
		t.Fatal("crashed_at deveria estar marcado")
	}
	if f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("o app em crash loop deve ficar parado (sem reinício infinito)")
	}
	if f.svc.State(f.ctx, got) != "crashed" {
		t.Fatalf("estado = %s", f.svc.State(f.ctx, got))
	}
	evs, _ := f.step.Events.List(f.ctx, 20, 0)
	var found bool
	for _, e := range evs {
		if e.Kind == "app.crash_loop" {
			found = true
			if !bytes.Contains(e.Data, []byte("boom: cannot find module")) {
				t.Fatalf("o evento deve trazer as últimas linhas do log: %s", e.Data)
			}
		}
	}
	if !found {
		t.Fatal("evento app.crash_loop ausente")
	}
	// continua parado nos ciclos seguintes; só ação explícita (Start) tira de crashed
	f.cycle(t)
	f.cycle(t)
	if f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("reconciliador não pode religar um app crashed")
	}
	if _, err := f.svc.Start(f.ctx, "web"); err != nil {
		t.Fatal(err)
	}
	f.cycle(t)
	got, _ = f.svc.Get(f.ctx, "web")
	if got.CrashedAt != nil || !f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("Start deveria limpar o crashed e subir o app")
	}
}

func TestOrphansRemovedOnlyAfterGrace(t *testing.T) {
	f := newFx(t)
	f.svc.Create(f.ctx, apps.CreateInput{Name: "web"})
	f.rt.Ensure(f.ctx, runit.ServiceSpec{Name: "dp-app-fantasma"})
	f.rt.Ensure(f.ctx, runit.ServiceSpec{Name: "dp-caddy"}) // fora do prefixo de apps: nunca é órfão
	for i := 0; i < 2; i++ {
		f.cycle(t)
		if _, ok := f.rt.Svcs["dp-app-fantasma"]; !ok {
			t.Fatalf("removido cedo demais (ciclo %d)", i)
		}
	}
	f.cycle(t)
	if _, ok := f.rt.Svcs["dp-app-fantasma"]; ok {
		t.Fatal("órfão deveria ter sido removido no 3º ciclo")
	}
	if _, ok := f.rt.Svcs["dp-caddy"]; !ok || f.rt.Svcs["dp-app-web"] == nil {
		t.Fatal("removeu serviço que não era órfão")
	}
}

func TestOrphanGraceResetsWhenOwnerReturns(t *testing.T) {
	f := newFx(t)
	f.rt.Ensure(f.ctx, runit.ServiceSpec{Name: "dp-app-volta"})
	f.cycle(t)
	f.cycle(t)
	f.svc.Create(f.ctx, apps.CreateInput{Name: "volta"}) // o dono aparece antes do prazo
	f.cycle(t)
	f.cycle(t)
	f.cycle(t)
	if _, ok := f.rt.Svcs["dp-app-volta"]; !ok {
		t.Fatal("serviço de app existente foi removido")
	}
}

func TestOwnRestartsAreNotCrashes(t *testing.T) {
	f := newFx(t)
	a, _ := f.svc.Create(f.ctx, apps.CreateInput{Name: "web"})
	f.release(t, a, "node")
	f.cycle(t) // sobe
	// 10 reinícios pedidos pelo próprio DroidPaaS (deploys/restarts), cada um absorvido antes do ciclo seguinte
	for i := 0; i < 10; i++ {
		f.rt.Crash("dp-app-web")
		f.svc.Touch(a.ID)
		f.cycle(t)
	}
	got, _ := f.svc.Get(f.ctx, "web")
	if got.CrashedAt != nil || !f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("reinícios causados pelo DroidPaaS não podem virar crash loop")
	}
	// mas quedas reais depois disso continuam sendo detectadas
	for i := 0; i < 8; i++ {
		f.rt.Crash("dp-app-web")
		f.cycle(t)
	}
	if got, _ = f.svc.Get(f.ctx, "web"); got.CrashedAt == nil {
		t.Fatal("o crash loop real deveria ser detectado")
	}
}
