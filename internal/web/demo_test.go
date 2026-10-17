//go:build demo

package web_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"testing"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/system"
)

// Servidor de demonstração do painel com dados fictícios, para inspeção visual:
//
//	go test -tags demo -run TestDemo -timeout 0 ./internal/web   (usuário admin, senha: senha-de-teste-123, porta 18999)
func TestDemo(t *testing.T) {
	p := newPanel(t)
	ctx := t.Context()
	svc := p.env.Srv.Apps
	for _, n := range []string{"minha-api", "blog", "worker-teste", "loja"} {
		if _, err := svc.Create(ctx, apps.CreateInput{Name: n}); err != nil {
			t.Fatal(err)
		}
	}
	p.env.Srv.Store.SetSetting(ctx, "base_domain", "apps.exemplo.com")
	svc.SyncAutoDomains(ctx)
	svc.AddDomain(ctx, "loja", "loja.exemplo.com.br")
	for i, n := range []string{"minha-api", "blog", "loja"} {
		a, _ := svc.Get(ctx, n)
		rt := []string{"node", "static", "python"}[i]
		p.env.Srv.Store.DB.Exec(`insert into releases(app_id,git_sha,git_ref,message,status,path,runtime,created_at) values(?,?,?,?,?,?,?,?)`,
			a.ID, "3f2a9c1deadbeef0000000000000000000000000", "main", "fix: timeout do cliente", "ready", "/x/"+n, rt, time.Now().Add(-2*time.Hour).UnixMilli())
		var rid int64
		p.env.Srv.Store.DB.QueryRow(`select max(id) from releases`).Scan(&rid)
		p.env.Srv.Store.SetCurrentRelease(ctx, a.ID, &rid)
		p.env.Srv.Store.DB.Exec(`insert into deploys(app_id,release_id,trigger,status,log_path,git_sha,git_ref,queued_at,started_at,finished_at) values(?,?,?,?,?,?,?,?,?,?)`,
			a.ID, rid, "git_push", "succeeded", "", "3f2a9c1deadbeef", "main", time.Now().Add(-2*time.Hour).UnixMilli(), time.Now().Add(-2*time.Hour).UnixMilli(), time.Now().Add(-2*time.Hour).Add(48*time.Second).UnixMilli())
		p.env.Srv.Store.DB.Exec(`insert into deploys(app_id,trigger,status,error,log_path,git_sha,git_ref,queued_at,started_at,finished_at) values(?,?,?,?,?,?,?,?,?,?)`,
			a.ID, "git_push", "failed", "`npm ci` falhou: exit status 1", "", "b71e00a", "main", time.Now().Add(-5*time.Hour).UnixMilli(), time.Now().Add(-5*time.Hour).UnixMilli(), time.Now().Add(-5*time.Hour).Add(20*time.Second).UnixMilli())
	}
	svc.Start(ctx, "minha-api")
	svc.Start(ctx, "loja")
	now := time.Now().UnixMilli()
	crashed, _ := svc.Get(ctx, "worker-teste")
	p.env.Srv.Store.SetCrashed(ctx, crashed.ID, &now)
	svc.EnvApply(ctx, "minha-api", map[string]apps.EnvValue{"LOG_LEVEL": {Value: "info"}, "DATABASE_URL": {Value: "postgres://u:senha@h/db", Secret: true}}, nil)
	p.probe.Procs[p.env.Runit.Svcs["dp-app-minha-api"].PID] = system.ProcStats{RSSKB: 58 * 1024, CPUTicks: 100, Procs: 2}
	p.col.Sample(ctx)
	p.env.Srv.Events.Info(ctx, "deploy.succeeded", nil, "deploy #1 succeeded", nil)
	p.env.Srv.Events.Error(ctx, "app.crash_loop", &crashed.ID, "worker-teste reiniciou 5 vezes em 1m0s e foi parado", map[string]any{"ultimas_linhas": []string{"Error: Cannot find module 'x'"}})
	p.env.Srv.Events.Warn(ctx, "proxy.apply_failed", nil, "falha ao aplicar a config no Caddy", nil)

	ln, err := net.Listen("tcp", "127.0.0.1:18999")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("painel de demonstração em http://127.0.0.1:18999  (admin / " + adminPass + ")")
	go http.Serve(ln, p.ts.Config.Handler)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
