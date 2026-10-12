package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/events"
	"droidpaas/internal/logs"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

// CrashTracker conta reinícios por app numa janela deslizante (TSD §8.2.5). Um reinício é detectado quando o PID
// do serviço muda entre dois ciclos.
type CrashTracker struct {
	Window    time.Duration
	Threshold int
	apps      map[string]*track
}

type track struct {
	pid      int
	restarts []time.Time
}

func NewCrashTracker(window time.Duration, threshold int) *CrashTracker {
	return &CrashTracker{Window: window, Threshold: threshold, apps: map[string]*track{}}
}

// Observe registra o estado atual; devolve true quando o app entrou em crash loop (e zera o histórico dele).
func (c *CrashTracker) Observe(name string, st runit.ServiceStatus, now time.Time) bool {
	t := c.apps[name]
	if t == nil {
		t = &track{}
		c.apps[name] = t
	}
	if st.State != "run" {
		t.pid = 0
		return false
	}
	if t.pid != 0 && st.PID != t.pid {
		t.restarts = append(t.restarts, now)
	}
	t.pid = st.PID
	cut := now.Add(-c.Window)
	keep := t.restarts[:0]
	for _, r := range t.restarts {
		if r.After(cut) {
			keep = append(keep, r)
		}
	}
	t.restarts = keep
	if len(t.restarts) >= c.Threshold {
		delete(c.apps, name)
		return true
	}
	return false
}

func (c *CrashTracker) Forget(name string) { delete(c.apps, name) }

// Apps é o passo "apps" do reconciliador (§8.7): converge o runit ao estado desejado, detecta crash loop e
// remove serviços órfãos depois de um período de graça.
type Apps struct {
	Store     *store.Store
	Svc       *apps.Service
	Runit     runit.ServiceManager
	Events    *events.Log
	Log       *slog.Logger
	Deploying func(appID int64) bool
	Tracker   *CrashTracker
	Now       func() time.Time
	// OrphanGrace é quantos ciclos seguidos um serviço precisa ficar sem dono antes de ser removido.
	OrphanGrace int
	missing     map[string]int
}

func (a *Apps) Run(ctx context.Context) error {
	if a.Now == nil {
		a.Now = time.Now
	}
	if a.missing == nil {
		a.missing = map[string]int{}
	}
	if a.OrphanGrace == 0 {
		a.OrphanGrace = 3
	}
	list, err := a.Store.ListApps(ctx)
	if err != nil {
		return err // sem ler o banco não dá para saber o que é órfão: não mexe em nada
	}
	known := map[string]bool{}
	var errs []error
	for _, app := range list {
		known[a.Svc.ServiceName(app.Name)] = true
		if a.Deploying != nil && a.Deploying(app.ID) {
			continue // o deploy conduz o serviço; o reconciliador não compete com ele
		}
		if err := a.reconcileApp(ctx, app); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", app.Name, err))
		}
	}
	if err := a.sweepOrphans(ctx, known); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (a *Apps) reconcileApp(ctx context.Context, app store.App) error {
	name := a.Svc.ServiceName(app.Name)
	wantUp := app.DesiredState == "running" && app.CurrentReleaseID != nil && app.CrashedAt == nil
	if wantUp {
		rel, err := a.Store.Release(ctx, *app.CurrentReleaseID)
		if err != nil {
			return err
		}
		wantUp = rel.Runtime != "static" // site estático não tem processo
	}
	if err := a.Runit.Ensure(ctx, a.Svc.Spec(app, !wantUp)); err != nil {
		return err
	}
	st, err := a.Runit.Status(ctx, name)
	if err != nil {
		return err
	}
	switch {
	case wantUp && st.State == "down":
		return a.Runit.Up(ctx, name)
	case !wantUp && st.State == "run":
		a.Tracker.Forget(name)
		return a.Runit.Down(ctx, name)
	case !wantUp:
		a.Tracker.Forget(name)
		return nil
	}
	if a.Tracker.Observe(name, st, a.Now()) {
		return a.markCrashed(ctx, app, name)
	}
	return nil
}

// markCrashed para o reinício infinito: cria `down`, grava crashed_at e registra as últimas linhas do log (§8.2.5).
func (a *Apps) markCrashed(ctx context.Context, app store.App, name string) error {
	if err := a.Runit.Down(ctx, name); err != nil {
		return err
	}
	now := a.Now().UnixMilli()
	if err := a.Store.SetCrashed(ctx, app.ID, &now); err != nil {
		return err
	}
	var tail []string
	if ls, err := logs.Tail(a.Svc.LogDir(app.Name), 20); err == nil {
		for _, l := range ls {
			tail = append(tail, l.Text)
		}
	}
	a.Events.Error(ctx, "app.crash_loop", &app.ID, fmt.Sprintf("%s reiniciou %d vezes em %s e foi parado", app.Name, a.Tracker.Threshold, a.Tracker.Window),
		map[string]any{"ultimas_linhas": tail})
	return nil
}

func (a *Apps) sweepOrphans(ctx context.Context, known map[string]bool) error {
	prefix := a.Svc.Cfg.Runit.Prefix + "app-"
	names, err := a.Runit.List(ctx, prefix)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, n := range names {
		if known[n] {
			continue
		}
		present[n] = true
		a.missing[n]++
		if a.missing[n] >= a.OrphanGrace {
			if err := a.Runit.Remove(ctx, n); err != nil {
				return err
			}
			delete(a.missing, n)
			a.Events.Warn(ctx, "service.orphan_removed", nil, "serviço sem app removido: "+n, nil)
		}
	}
	for n := range a.missing {
		if !present[n] {
			delete(a.missing, n) // voltou a ter dono (ou já foi removido)
		}
	}
	return nil
}
