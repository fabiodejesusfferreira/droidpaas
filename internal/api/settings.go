package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
)

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`)

// settingValidators define as chaves editáveis por /settings. O token do túnel tem rota própria.
var settingValidators = map[string]func(string) bool{
	"base_domain":      func(v string) bool { return v == "" || hostRe.MatchString(v) },
	"panel_hostname":   func(v string) bool { return v == "" || hostRe.MatchString(v) },
	"keep_releases":    func(v string) bool { n, err := strconv.Atoi(v); return err == nil && n >= 1 && n <= 50 },
	"git_http_enabled": func(v string) bool { return v == "true" || v == "false" },
}

// EffectiveSettings devolve as configurações editáveis (valores gravados sobre os padrões do droidd.toml).
func (s *Server) EffectiveSettings(ctx context.Context) (map[string]string, error) {
	stored, err := s.Store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{
		"base_domain":      s.Cfg.Proxy.BaseDomain,
		"panel_hostname":   s.Cfg.Proxy.PanelHostname,
		"keep_releases":    strconv.Itoa(s.Cfg.Deploy.KeepReleases),
		"git_http_enabled": strconv.FormatBool(s.Cfg.Git.HTTPEnabled),
	}
	for k := range settingValidators {
		if v, ok := stored[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (s *Server) registerSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/settings", s.full(func(w http.ResponseWriter, r *http.Request) {
		m, err := s.EffectiveSettings(r.Context())
		if err != nil {
			s.internalErr(w, err)
			return
		}
		_, has, _ := s.Store.Setting(r.Context(), "tunnel_token")
		writeJSON(w, 200, map[string]any{"settings": m, "tunnel_configured": has})
	}))
	mux.HandleFunc("PUT /api/v1/settings", s.full(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if !decode(w, r, &in) {
			return
		}
		if err := s.ApplySettings(r.Context(), in); err != nil {
			s.appErr(w, err)
			return
		}
		m, _ := s.EffectiveSettings(r.Context())
		writeJSON(w, 200, map[string]any{"settings": m})
	}))
	mux.HandleFunc("PUT /api/v1/settings/tunnel", s.full(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := s.SetTunnelToken(r.Context(), in.Token); err != nil {
			s.appErr(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
