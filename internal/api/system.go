package api

import (
	"net"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"droidpaas/internal/runit"
	"droidpaas/internal/stats"
	"droidpaas/internal/tunnel"
)

// handleSystem: versões, uptime e estado dos serviços base (TSD §14.2). As métricas do aparelho (bateria, RAM,
// disco) entram na Fase 3 pela interface DeviceProbe.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	svc := map[string]string{"droidd": "run"}
	if s.Runit != nil {
		for _, n := range []string{"caddy", "cloudflared"} {
			name := s.Cfg.Runit.Prefix + n
			st, err := s.Runit.Status(ctx, name)
			switch {
			case err == runit.ErrNoService:
				svc[name] = "absent"
			case err != nil:
				svc[name] = "unknown"
			default:
				svc[name] = st.State
			}
		}
	}
	tun := "not_configured"
	if s.Runit != nil {
		tun = tunnel.State(ctx, s.Cfg, s.Store, s.Runit)
	}
	svc["sshd"] = "down"
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Cfg.Git.SSHPort)), time.Second); err == nil {
		c.Close()
		svc["sshd"] = "run"
	}
	counts := map[string]int{}
	total := 0
	if list, err := s.Apps.List(ctx); err == nil {
		for _, a := range list {
			counts[s.Apps.State(ctx, a)]++
			total++
		}
	}
	var device any
	if s.Stats != nil {
		device = deviceJSON(s.Stats.Device())
	}
	writeJSON(w, 200, map[string]any{
		"device":  device,
		"version": s.Version, "go": runtime.Version(), "uptime_s": int(time.Since(s.Started).Seconds()),
		"services": svc, "apps": map[string]any{"total": total, "by_state": counts},
		"proxy":  map[string]string{"listen": s.Cfg.Proxy.Listen, "admin": s.Cfg.Proxy.Admin},
		"tunnel": tun,
	})
}

func deviceJSON(d stats.Device) map[string]any {
	out := map[string]any{"at": d.At, "mem_total_kb": d.Mem.TotalKB, "mem_available_kb": d.Mem.AvailableKB,
		"disk_total_bytes": d.Disk.TotalBytes, "disk_free_bytes": d.Disk.FreeBytes, "battery": nil}
	if d.Battery != nil {
		out["battery"] = map[string]any{"percent": d.Battery.Percent, "temp_c": d.Battery.TempC, "charging": d.Battery.Charging, "health": d.Battery.Health}
	}
	return out
}

// handleAppStats: RSS/CPU atual e histórico recente do app (TSD §10.2, RF-42).
func (s *Server) handleAppStats(w http.ResponseWriter, r *http.Request) {
	a, err := s.Apps.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		s.appErr(w, err)
		return
	}
	if s.Stats == nil {
		writeJSON(w, 200, stats.AppStats{Series: []stats.Point{}})
		return
	}
	writeJSON(w, 200, s.Stats.App(a.ID))
}
