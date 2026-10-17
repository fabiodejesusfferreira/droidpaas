package stats

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
	"droidpaas/internal/system"
)

func setup(t *testing.T) (*Collector, *runit.Fake, *system.FakeProbe, store.App, *time.Time) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	rt := runit.NewFake()
	probe := &system.FakeProbe{Bat: system.BatteryInfo{Percent: 80, TempC: 33}, Mem: system.MemInfo{TotalKB: 4000, AvailableKB: 1500},
		Dsk: system.DiskInfo{TotalBytes: 100, FreeBytes: 40}, Procs: map[int]system.ProcStats{}}
	a := store.App{Name: "web", Runtime: "auto", Branch: "main", HealthTimeoutS: 30, DesiredState: "running"}
	st.InsertApp(ctx, &a)
	rt.Ensure(ctx, runit.ServiceSpec{Name: "dp-app-web"})
	now := time.Unix(1000, 0)
	c := New(st, rt, probe, cfg)
	c.Now = func() time.Time { return now }
	return c, rt, probe, a, &now
}

func TestDeviceSnapshotAndUnavailableBattery(t *testing.T) {
	c, _, probe, _, _ := setup(t)
	c.Sample(context.Background())
	d := c.Device()
	if d.Battery == nil || d.Battery.Percent != 80 || d.Mem.AvailableKB != 1500 || d.Disk.FreeBytes != 40 {
		t.Fatalf("%+v", d)
	}
	probe.BatErr = errors.New("sem Termux:API")
	c.Sample(context.Background())
	if d := c.Device(); d.Battery != nil || d.BatteryErr == "" || d.Mem.TotalKB == 0 {
		t.Fatalf("bateria indisponível não pode derrubar o resto: %+v", d)
	}
}

func TestPerAppCPUAndRing(t *testing.T) {
	c, rt, probe, a, now := setup(t)
	ctx := context.Background()
	pid := 1001 // o Fake atribui 1000+starts
	rt.Up(ctx, "dp-app-web")
	probe.Procs[pid] = system.ProcStats{RSSKB: 30000, CPUTicks: 100, Procs: 2}
	c.Sample(ctx)
	if cur := c.App(a.ID).Current; cur == nil || cur.RSSKB != 30000 || cur.CPUPct != 0 {
		t.Fatalf("1ª amostra não tem CPU (sem referência): %+v", cur)
	}
	*now = now.Add(30 * time.Second)
	probe.Procs[pid] = system.ProcStats{RSSKB: 31000, CPUTicks: 100 + 1500, Procs: 2} // 1500 ticks em 30 s = 50 ticks/s = 50 %
	c.Sample(ctx)
	cur := c.App(a.ID).Current
	if math.Abs(cur.CPUPct-50) > 0.001 || cur.RSSKB != 31000 {
		t.Fatalf("%+v", cur)
	}
	// o contador de ticks voltou (processo reiniciou): não pode dar CPU negativa
	*now = now.Add(30 * time.Second)
	probe.Procs[pid] = system.ProcStats{RSSKB: 1, CPUTicks: 5, Procs: 1}
	c.Sample(ctx)
	if cur := c.App(a.ID).Current; cur.CPUPct != 0 {
		t.Fatalf("%+v", cur)
	}
	// o ring guarda no máximo 2 h
	for i := 0; i < MaxPoints+50; i++ {
		*now = now.Add(30 * time.Second)
		c.Sample(ctx)
	}
	if n := len(c.App(a.ID).Series); n != MaxPoints {
		t.Fatalf("série com %d pontos", n)
	}
	// app parado: histórico some e Current é nil
	rt.Down(ctx, "dp-app-web")
	c.Sample(ctx)
	if s := c.App(a.ID); s.Current != nil || len(s.Series) != 0 {
		t.Fatalf("app parado deve zerar o histórico: %+v", s)
	}
}

func TestProcessStatsFailureSkipsApp(t *testing.T) {
	c, rt, _, a, _ := setup(t)
	rt.Up(context.Background(), "dp-app-web") // o probe não conhece o pid
	c.Sample(context.Background())
	if c.App(a.ID).Current != nil {
		t.Fatal("sem estatística do processo não há ponto")
	}
}
