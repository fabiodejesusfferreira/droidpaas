package api

import (
	"net"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"droidpaas/internal/runit"
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
	writeJSON(w, 200, map[string]any{
		"version": s.Version, "go": runtime.Version(), "uptime_s": int(time.Since(s.Started).Seconds()),
		"services": svc, "apps": map[string]any{"total": total, "by_state": counts},
		"proxy": map[string]string{"listen": s.Cfg.Proxy.Listen, "admin": s.Cfg.Proxy.Admin},
	})
}
