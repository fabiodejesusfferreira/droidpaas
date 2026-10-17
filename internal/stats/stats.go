// Package stats mantém em memória o estado do aparelho e o uso de recursos por app (TSD §8.10):
// amostragem leve a cada 30 s, histórico curto em ring buffer (2 h), nada gravado no SQLite.
package stats

import (
	"context"
	"sync"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
	"droidpaas/internal/system"
)

type Device struct {
	At         time.Time
	Battery    *system.BatteryInfo // nil = indisponível (Termux:API ausente, sem permissão ou lento)
	BatteryErr string
	Mem        system.MemInfo
	Disk       system.DiskInfo
}

type Point struct {
	T      time.Time `json:"t"`
	RSSKB  int64     `json:"rss_kb"`
	CPUPct float64   `json:"cpu_pct"`
	Procs  int       `json:"procs"`
}

type AppStats struct {
	Current *Point  `json:"current"`
	Series  []Point `json:"series"`
}

const (
	MaxPoints = 240 // 2 h a cada 30 s
)

type ring struct {
	pts       []Point
	lastTicks uint64
	lastAt    time.Time
	have      bool
}

type Collector struct {
	Store    *store.Store
	Runit    runit.ServiceManager
	Probe    system.DeviceProbe
	Cfg      config.Config
	Interval time.Duration
	Now      func() time.Time

	mu     sync.RWMutex
	dev    Device
	series map[int64]*ring
}

func New(st *store.Store, rt runit.ServiceManager, p system.DeviceProbe, cfg config.Config) *Collector {
	return &Collector{Store: st, Runit: rt, Probe: p, Cfg: cfg, Interval: 30 * time.Second, Now: time.Now, series: map[int64]*ring{}}
}

// Run amostra agora e depois a cada Interval, até ctx terminar.
func (c *Collector) Run(ctx context.Context) {
	c.Sample(ctx)
	t := time.NewTicker(c.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Sample(ctx)
		}
	}
}

// Sample lê o aparelho e cada app em execução. Falhas pontuais viram "indisponível", nunca erro fatal.
func (c *Collector) Sample(ctx context.Context) {
	now := c.Now()
	d := Device{At: now}
	if b, err := c.Probe.Battery(ctx); err == nil {
		d.Battery = &b
	} else {
		d.BatteryErr = err.Error()
	}
	d.Mem, _ = c.Probe.Memory(ctx)
	d.Disk, _ = c.Probe.Disk(ctx, c.Cfg.Paths.Home)

	apps, _ := c.Store.ListApps(ctx)
	live := map[int64]bool{}
	for _, a := range apps {
		st, err := c.Runit.Status(ctx, c.Cfg.Runit.Prefix+"app-"+a.Name)
		if err != nil || st.State != "run" || st.PID <= 0 {
			continue
		}
		ps, err := c.Probe.ProcessStats(ctx, st.PID)
		if err != nil {
			continue
		}
		live[a.ID] = true
		c.record(a.ID, now, ps)
	}
	c.mu.Lock()
	c.dev = d
	for id := range c.series { // app removido ou parado: o histórico dele sai da memória
		if !live[id] {
			delete(c.series, id)
		}
	}
	c.mu.Unlock()
}

func (c *Collector) record(id int64, now time.Time, ps system.ProcStats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.series[id]
	if r == nil {
		r = &ring{}
		c.series[id] = r
	}
	pt := Point{T: now, RSSKB: ps.RSSKB, Procs: ps.Procs}
	// CPU% = ticks consumidos desde a amostra anterior / (tempo decorrido × ticks por segundo); um núcleo = 100 %
	if r.have && ps.CPUTicks >= r.lastTicks && now.After(r.lastAt) {
		pt.CPUPct = float64(ps.CPUTicks-r.lastTicks) / (now.Sub(r.lastAt).Seconds() * system.ClockTicksPerSecond) * 100
	}
	r.lastTicks, r.lastAt, r.have = ps.CPUTicks, now, true
	r.pts = append(r.pts, pt)
	if len(r.pts) > MaxPoints {
		r.pts = r.pts[len(r.pts)-MaxPoints:]
	}
}

func (c *Collector) Device() Device {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dev
}

// App devolve o ponto atual e o histórico; Current é nil quando o app não está em execução.
func (c *Collector) App(id int64) AppStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r := c.series[id]
	if r == nil || len(r.pts) == 0 {
		return AppStats{Series: []Point{}}
	}
	cur := r.pts[len(r.pts)-1]
	return AppStats{Current: &cur, Series: append([]Point(nil), r.pts...)}
}
