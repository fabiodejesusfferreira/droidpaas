package web

import (
	"net/http"
	"strings"
	"time"

	"droidpaas/internal/stats"
	"droidpaas/internal/store"
	"droidpaas/internal/tunnel"
)

type appCard struct {
	App      store.App
	State    string
	Runtime  string
	Domains  []string
	URL      string
	Release  *store.Release
	Stats    *stats.Point
	Deployed time.Time
}

type deviceView struct {
	Ready       bool
	HasBattery  bool
	BatteryNote string
	Percent     int
	TempC       float64
	Charging    bool
	MemAvailMB  int64
	MemTotalMB  int64
	DiskUsedPct int
	DiskFreeGB  float64
	Hot         bool // acima de 42 °C (limite sugerido do PoC): só alerta, nenhuma ação automática
}

const hotC = 42.0

func (s *Server) deviceView() deviceView {
	if s.Stats == nil {
		return deviceView{}
	}
	d := s.Stats.Device()
	if d.At.IsZero() {
		return deviceView{}
	}
	v := deviceView{Ready: true, MemAvailMB: d.Mem.AvailableKB / 1024, MemTotalMB: d.Mem.TotalKB / 1024}
	if d.Battery != nil {
		v.HasBattery, v.Percent, v.TempC, v.Charging = true, d.Battery.Percent, d.Battery.TempC, d.Battery.Charging
		v.Hot = d.Battery.TempC >= hotC
	} else {
		v.BatteryNote = "device.battery.unavailable"
	}
	if d.Disk.TotalBytes > 0 {
		v.DiskUsedPct = int(100 * (d.Disk.TotalBytes - d.Disk.FreeBytes) / d.Disk.TotalBytes)
		v.DiskFreeGB = float64(d.Disk.FreeBytes) / (1 << 30)
	}
	return v
}

// publicScheme: com o túnel configurado os apps são servidos em HTTPS pela Cloudflare.
func (s *Server) publicScheme(c *ctx) string {
	if _, ok, _ := s.API.Store.Setting(c.r.Context(), "tunnel_token"); ok {
		return "https"
	}
	return "http"
}

func (s *Server) cards(c *ctx) ([]appCard, error) {
	ctx := c.r.Context()
	list, err := s.API.Apps.List(ctx)
	if err != nil {
		return nil, err
	}
	doms, err := s.API.Store.Domains(ctx, 0)
	if err != nil {
		return nil, err
	}
	byApp := map[int64][]string{}
	for _, d := range doms {
		byApp[d.AppID] = append(byApp[d.AppID], d.Hostname)
	}
	scheme := s.publicScheme(c)
	out := make([]appCard, 0, len(list))
	for _, a := range list {
		out = append(out, s.card(c, a, byApp[a.ID], scheme))
	}
	return out, nil
}

func (s *Server) card(c *ctx, a store.App, domains []string, scheme string) appCard {
	ctx := c.r.Context()
	cd := appCard{App: a, State: s.API.Apps.State(ctx, a), Runtime: a.Runtime, Domains: domains}
	if len(domains) > 0 {
		cd.URL = scheme + "://" + domains[0]
	}
	if a.CurrentReleaseID != nil {
		if rel, err := s.API.Store.Release(ctx, *a.CurrentReleaseID); err == nil {
			cd.Release = &rel
			cd.Deployed = time.UnixMilli(rel.CreatedAt)
			if cd.Runtime == "auto" || cd.Runtime == "" {
				cd.Runtime = rel.Runtime
			}
		}
	}
	if s.Stats != nil {
		cd.Stats = s.Stats.App(a.ID).Current
	}
	return cd
}

type dashData struct {
	Cards  []appCard
	Device deviceView
}

func (s *Server) dashboard(c *ctx) {
	cards, err := s.cards(c)
	if err != nil {
		c.fail(http.StatusInternalServerError, "err.generic")
		return
	}
	v := c.view(T("nav.apps"), "apps")
	v.Data = dashData{Cards: cards, Device: s.deviceView()}
	c.render(http.StatusOK, "dashboard", v)
}

func (s *Server) fragApps(c *ctx) {
	cards, err := s.cards(c)
	if err != nil {
		http.Error(c.w, "erro", http.StatusInternalServerError)
		return
	}
	c.fragment("apps_list", cards)
}

func (s *Server) fragDevice(c *ctx) { c.fragment("device", s.deviceView()) }

func (s *Server) tunnelState(c *ctx) string {
	if s.API.Runit == nil {
		return "not_configured"
	}
	return tunnel.State(c.r.Context(), s.API.Cfg, s.API.Store, s.API.Runit)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
