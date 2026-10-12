package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"droidpaas/internal/api"
	"droidpaas/internal/apps"
	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/deploy"
	"droidpaas/internal/events"
	"droidpaas/internal/gitsrv"
	"droidpaas/internal/proxy"
	"droidpaas/internal/reconcile"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
	"droidpaas/internal/system"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	home := fs.String("home", defaultHome(), "diretório base ($DP_HOME)")
	cfgPath := fs.String("config", "", "droidd.toml (padrão: <home>/etc/droidd.toml)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		c := config.Default()
		c.SetHome(*home)
		*cfgPath = c.File()
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(64 << 20) // TSD §8.1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	interrupted, queued, err := st.InterruptRunningDeploys(ctx)
	if err != nil {
		return err
	}
	log.Info("banco pronto", "db", cfg.DBPath(), "deploys_interrompidos", interrupted, "deploys_na_fila", queued)

	box, err := crypt.Load(cfg.KeyPath())
	if err != nil {
		return err
	}
	ev := &events.Log{DB: st.DB, Log: log}
	gs := &gitsrv.Service{Store: st, Cfg: cfg}
	svc := &apps.Service{Git: gs, Store: st, Runit: runit.New(cfg.Runit.ServiceDir), Box: box, Events: ev, Cfg: cfg}
	srv := &api.Server{Store: st, Events: ev, Box: box, Cfg: cfg, Version: version, Log: log, Apps: svc, Git: gs}
	rt := runit.New(cfg.Runit.ServiceDir)
	eng := deploy.New(&deploy.Engine{Store: st, Apps: svc, Runit: rt, Git: system.GitCLI{}, Runner: system.ExecRunner{}, Cfg: cfg, Events: ev, Log: log})
	svc.Deploying = eng.IsDeploying
	srv.Deploy = eng
	loop := reconcile.New(cfg.Reconcile.Interval.D(), log,
		reconcile.Step{Name: "domains", Run: svc.SyncAutoDomains},
		reconcile.Step{Name: "git-keys", Run: gs.SyncKeys},
		reconcile.Step{Name: "apps", Run: (&reconcile.Apps{Store: st, Svc: svc, Runit: rt, Events: ev, Log: log, Deploying: eng.IsDeploying,
			Tracker: reconcile.NewCrashTracker(cfg.Reconcile.CrashLoopWindow.D(), cfg.Reconcile.CrashLoopThreshold)}).Run},
		reconcile.Step{Name: "logs", Run: reconcile.Every(time.Hour, eng.PruneLogs)},
		reconcile.Step{Name: "proxy", Run: proxy.NewController(st, cfg, rt, proxy.NewClient(cfg.Proxy.Admin), ev).Step})
	svc.Wake, srv.OnChange, eng.Wake = loop.Wake, loop.Wake, loop.Wake
	go eng.Run(ctx)
	go loop.Run(ctx)

	pub := &http.Server{Addr: cfg.HTTP.PanelAddr, Handler: srv.Public(), ReadHeaderTimeout: 10 * time.Second}
	internal := &http.Server{Handler: srv.Internal(), ReadHeaderTimeout: 10 * time.Second}

	sock := cfg.SocketPath()
	os.Remove(sock) // socket velho de uma queda anterior
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}

	errc := make(chan error, 2)
	go func() { errc <- ignoreClosed(pub.ListenAndServe()) }()
	go func() { errc <- ignoreClosed(internal.Serve(ln)) }()
	log.Info("droidd no ar", "painel", cfg.HTTP.PanelAddr, "socket", sock, "versao", version)

	select {
	case <-ctx.Done():
		log.Info("encerrando (SIGTERM); apps, proxy e túnel seguem no ar")
	case err := <-errc:
		if err != nil {
			stop()
			return err
		}
	}
	// Só o droidd para: nunca derruba apps/Caddy/túnel (RNF-08).
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pub.Shutdown(sctx)
	internal.Shutdown(sctx)
	os.Remove(sock)
	return nil
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newLogger(cfg config.Config) *slog.Logger {
	var lvl slog.Level
	lvl.UnmarshalText([]byte(cfg.Logging.Level))
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.Logging.Format == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}
