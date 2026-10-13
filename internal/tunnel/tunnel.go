// Package tunnel mantém o serviço dp-cloudflared (modo token) conforme o token configurado (TSD §8.6, ADR-06).
package tunnel

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/events"
	"droidpaas/internal/logs"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

type Controller struct {
	Store  *store.Store
	Cfg    config.Config
	Runit  runit.ServiceManager
	Box    *crypt.Box
	Events *events.Log
	last   [32]byte
	have   bool
}

func ServiceName(cfg config.Config) string { return cfg.Runit.Prefix + "cloudflared" }
func envDir(cfg config.Config) string      { return cfg.Dir("etc", "tunnel-env") }

// Spec: o token nunca entra no script nem na linha de comando (`ps`): vai para um envdir 0700 lido pelo chpst (Q-03).
func Spec(cfg config.Config) runit.ServiceSpec {
	prefix := runit.Prefix()
	logDir := cfg.Dir("logs", "cloudflared")
	run := "#!" + prefix + "/bin/sh\nexec 2>&1\nexport PATH=" + prefix + "/bin:$PATH\n" +
		"exec chpst -e " + envDir(cfg) + " cloudflared tunnel --no-autoupdate run\n"
	return runit.ServiceSpec{Name: ServiceName(cfg), Run: run, LogRun: runit.LogRun(prefix, logDir), LogDir: logDir, LogConfig: runit.LogConfig(1_000_000, 3)}
}

func (c *Controller) token(ctx context.Context) (string, error) {
	v, ok, err := c.Store.Setting(ctx, "tunnel_token")
	if err != nil || !ok {
		return "", err
	}
	plain, err := c.Box.Open([]byte(v))
	return strings.TrimSpace(string(plain)), err
}

// Step é o passo "tunnel" do reconciliador.
func (c *Controller) Step(ctx context.Context) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	name := ServiceName(c.Cfg)
	if tok == "" { // token removido: o serviço e o arquivo do token somem
		if c.have {
			c.Events.Info(ctx, "tunnel.stopped", nil, "túnel desligado", nil)
		}
		c.have = false
		os.RemoveAll(envDir(c.Cfg))
		return c.Runit.Remove(ctx, name)
	}
	sum := sha256.Sum256([]byte(tok))
	changed := !c.have || sum != c.last
	if err := os.MkdirAll(envDir(c.Cfg), 0o700); err != nil {
		return err
	}
	path := filepath.Join(envDir(c.Cfg), "TUNNEL_TOKEN")
	if err := os.WriteFile(path+".tmp", []byte(tok), 0o600); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	if err := c.Runit.Ensure(ctx, Spec(c.Cfg)); err != nil {
		return err
	}
	st, err := c.Runit.Status(ctx, name)
	if err != nil {
		return err
	}
	switch {
	case st.State == "down":
		err = c.Runit.Up(ctx, name)
	case changed && c.have: // o token mudou com o túnel rodando: reinicia para ler o novo
		err = c.Runit.Restart(ctx, name, 10*time.Second)
	}
	if err != nil {
		return err
	}
	if changed {
		c.Events.Info(ctx, "tunnel.started", nil, "túnel configurado", nil)
	}
	c.last, c.have = sum, true
	return nil
}

// State resume o túnel para a API/UI: not_configured, down ou connected/connecting (pelo log do cloudflared).
func State(ctx context.Context, cfg config.Config, st *store.Store, rt runit.ServiceManager) string {
	if _, ok, _ := st.Setting(ctx, "tunnel_token"); !ok {
		return "not_configured"
	}
	s, err := rt.Status(ctx, ServiceName(cfg))
	if err != nil {
		if errors.Is(err, runit.ErrNoService) {
			return "down"
		}
		return "unknown"
	}
	if s.State != "run" {
		return "down"
	}
	ls, _ := logs.Tail(cfg.Dir("logs", "cloudflared"), 80)
	state := "connecting"
	for _, l := range ls { // a última palavra do log vence
		switch {
		case strings.Contains(l.Text, "Registered tunnel connection"):
			state = "connected"
		case strings.Contains(l.Text, "Unregistered tunnel connection"), strings.Contains(l.Text, "Retrying connection"):
			state = "connecting"
		}
	}
	return state
}
