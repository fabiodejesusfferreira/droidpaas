package proxy

import (
	"context"
	"errors"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

// Controller é o passo "proxy" do reconciliador: garante o dp-caddy e aplica a config desejada.
type Controller struct {
	Store   *store.Store
	Cfg     config.Config
	Runit   runit.ServiceManager
	Client  ProxyClient
	Events  *events.Log
	Pages   Pages
	applier *Applier
	lastPID int
	young   bool // o Caddy acabou de (re)iniciar: a Admin API pode ainda não estar escutando
}

func NewController(st *store.Store, cfg config.Config, rt runit.ServiceManager, client ProxyClient, ev *events.Log) *Controller {
	c := &Controller{Store: st, Cfg: cfg, Runit: rt, Client: client, Events: ev, Pages: LoadPages(cfg.Dir("proxy", "pages"))}
	c.applier = &Applier{Client: client, Now: time.Now, OnFail: func(err error, retry time.Duration) {
		if c.young {
			return // falha esperada logo depois do start; o próximo ciclo tenta de novo (e o Caddy já sobe com o caddy.json certo)
		}
		ev.Error(context.Background(), "proxy.apply_failed", nil, "falha ao aplicar a config no Caddy", map[string]any{"err": err.Error(), "retry_em": retry.String()})
	}}
	return c
}

func (c *Controller) panelHostname(ctx context.Context) string {
	if v, ok, _ := c.Store.Setting(ctx, "panel_hostname"); ok {
		return v
	}
	return c.Cfg.Proxy.PanelHostname
}

func (c *Controller) Step(ctx context.Context) error {
	in, err := BuildInput(ctx, c.Store, c.Cfg, c.panelHostname(ctx), c.Pages)
	if err != nil {
		return err
	}
	cfgJSON, err := Generate(in)
	if err != nil {
		return err
	}
	// o arquivo vem antes do serviço: o Caddy já nasce com as rotas certas
	if err := WriteConfigFile(c.Cfg, cfgJSON); err != nil {
		return err
	}
	spec := CaddySpec(c.Cfg)
	if err := c.Runit.Ensure(ctx, spec); err != nil {
		return err
	}
	st, err := c.Runit.Status(ctx, spec.Name)
	if errors.Is(err, runit.ErrNotReady) {
		return nil
	}
	if err != nil {
		return err
	}
	c.young = st.Uptime < 20*time.Second
	if st.State == "down" {
		if err := c.Runit.Up(ctx, spec.Name); err != nil {
			return err
		}
		return nil // a aplicação acontece no próximo ciclo, quando o Caddy já estiver de pé
	}
	if st.PID != c.lastPID { // o Caddy (re)iniciou: ele pode estar com a config do bootstrap (PoC V-04)
		c.lastPID = st.PID
		c.applier.Invalidate()
	}
	applied, err := c.applier.Apply(ctx, cfgJSON)
	if err != nil && c.young {
		return nil
	}
	if applied {
		c.Events.Info(ctx, "proxy.applied", nil, "config do Caddy aplicada", map[string]any{"rotas": len(in.Apps)})
	}
	return err
}
