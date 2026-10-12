package proxy

import (
	"context"
	"os"
	"path/filepath"

	"droidpaas/internal/config"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

// Input traduz o estado desejado (SQLite) nas rotas do Caddy.
func BuildInput(ctx context.Context, st *store.Store, cfg config.Config, panelHostname string, pages Pages) (Input, error) {
	apps, err := st.ListApps(ctx)
	if err != nil {
		return Input{}, err
	}
	doms, err := st.Domains(ctx, 0)
	if err != nil {
		return Input{}, err
	}
	hosts := map[int64][]string{}
	for _, d := range doms {
		hosts[d.AppID] = append(hosts[d.AppID], d.Hostname)
	}
	in := Input{Listen: cfg.Proxy.Listen, Admin: cfg.Proxy.Admin, PanelHostname: panelHostname, PanelAddr: cfg.HTTP.PanelAddr, Pages: pages}
	for _, a := range apps {
		r := AppRoute{Name: a.Name, Hosts: hosts[a.ID], Kind: Stopped}
		// só roteia para um app que está para rodar: desejado=running, com release e sem crash loop
		if a.DesiredState == "running" && a.CurrentReleaseID != nil && a.CrashedAt == nil {
			rel, err := st.Release(ctx, *a.CurrentReleaseID)
			if err != nil {
				return Input{}, err
			}
			switch {
			case rel.Runtime == "static":
				r.Kind = Static
				r.Root = filepath.Join(cfg.Dir("apps", a.Name, "current"), derefStr(a.StaticDir))
			case a.Port != nil:
				r.Kind, r.Port = Process, *a.Port
			}
		}
		in.Apps = append(in.Apps, r)
	}
	return in, nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// CaddySpec é o serviço runit dp-caddy. O `run` aponta para o arquivo de config que o droidd mantém
// atualizado: se o Caddy reiniciar sozinho (ou antes do droidd subir) ele já volta com as rotas certas.
func CaddySpec(cfg config.Config) runit.ServiceSpec {
	prefix := runit.Prefix()
	logDir := cfg.Dir("logs", "caddy")
	run := "#!" + prefix + "/bin/sh\nexec 2>&1\nexport PATH=" + prefix + "/bin:$PATH\n" +
		"export HOME=" + cfg.Paths.Home + "\n" +
		"export XDG_CONFIG_HOME=" + cfg.Dir("proxy", "config") + " XDG_DATA_HOME=" + cfg.Dir("proxy", "data") + "\n" +
		"export GOMEMLIMIT=128MiB\n" +
		"exec caddy run --config " + cfg.Dir("proxy", "caddy.json") + "\n"
	return runit.ServiceSpec{
		Name: cfg.Runit.Prefix + "caddy", Run: run,
		LogRun: runit.LogRun(prefix, logDir), LogDir: logDir, LogConfig: runit.LogConfig(1_000_000, 5),
	}
}

// WriteConfigFile grava de forma atômica a config que o dp-caddy lê ao subir.
func WriteConfigFile(cfg config.Config, b []byte) error {
	path := cfg.Dir("proxy", "caddy.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
