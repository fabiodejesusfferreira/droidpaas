package deploy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"droidpaas/internal/build"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

// job é a execução de um deploy.
type job struct {
	e    *Engine
	d    store.Deploy
	app  store.App
	out  *stream
	cs   *cancelState
	prev *store.Release // release ativa antes do deploy (alvo do rollback automático)
	rel  store.Release
	last string // último estágio anunciado
}

func (e *Engine) execute(parent context.Context, d store.Deploy) {
	ctx, cancel := context.WithCancel(parent)
	cs := &cancelState{cancel: cancel}
	e.mu.Lock()
	e.cancels[d.ID], e.active[d.AppID] = cs, d.ID
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		delete(e.cancels, d.ID)
		delete(e.active, d.AppID)
		e.mu.Unlock()
	}()

	bg := context.Background() // gravações finais precisam sobreviver ao cancelamento
	app, err := e.Store.AppByID(bg, d.AppID)
	if err != nil {
		e.Store.FinishDeploy(bg, d.ID, "failed", "app não encontrado")
		return
	}
	os.MkdirAll(e.logDir(app.Name), 0o700)
	out, err := e.hub.open(d.ID, d.LogPath)
	if err != nil {
		e.Store.FinishDeploy(bg, d.ID, "failed", "não consegui abrir o log: "+err.Error())
		return
	}
	j := &job{e: e, d: d, app: app, out: out, cs: cs}
	if app.CurrentReleaseID != nil {
		if r, err := e.Store.Release(bg, *app.CurrentReleaseID); err == nil {
			j.prev = &r
		}
	}
	started := time.Now()
	status, msg := j.run(ctx)
	if status == "cancelled" && parent.Err() != nil && !cs.user {
		status, msg = "interrupted", "o droidd foi encerrado durante o deploy"
	}
	j.say("")
	j.say("deploy #%d: %s em %s", d.ID, status, time.Since(started).Round(time.Second))
	if msg != "" {
		j.say("%s", msg)
	}
	e.Store.FinishDeploy(bg, d.ID, status, msg) // o status final vai ao banco antes de fechar o stream
	e.hub.finish(d.ID)
	level := e.Events.Info
	if status == "failed" || status == "rolled_back" {
		level = e.Events.Warn
	}
	level(bg, "deploy."+status, &app.ID, fmt.Sprintf("deploy #%d %s", d.ID, status), map[string]any{"deploy": d.ID, "sha": d.GitSHA, "error": msg})
	e.wake()
}

func (j *job) say(format string, a ...any) { fmt.Fprintf(j.out, format+"\n", a...) }

func (j *job) stage(name string) {
	if name == j.last {
		return
	}
	j.last = name
	j.e.Store.SetDeployStage(context.Background(), j.d.ID, name)
	j.say("-----> %s", name)
}

func (j *job) run(ctx context.Context) (status, msg string) {
	defer func() {
		if r := recover(); r != nil {
			j.discardRelease()
			status, msg = "failed", fmt.Sprintf("erro interno: %v", r)
		}
	}()
	if j.d.Trigger == "rollback" {
		return j.runRollback(ctx)
	}
	return j.runBuild(ctx)
}

// discardRelease apaga uma release que não chegou a ser ativada (a release de um rollback manual é preservada).
func (j *job) discardRelease() {
	if j.rel.ID == 0 || j.d.Trigger == "rollback" {
		return
	}
	j.rel.Status = "failed"
	j.e.Store.UpdateRelease(context.Background(), j.rel)
	if strings.Contains(j.rel.Path, string(filepath.Separator)+"releases"+string(filepath.Separator)) {
		os.RemoveAll(j.rel.Path)
	}
}

// failBefore trata falhas antes da troca: a release em execução não é tocada (RNF-09).
func (j *job) failBefore(ctx context.Context, err error, what string) (string, string) {
	j.discardRelease()
	switch {
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled):
		return "cancelled", "cancelado"
	case errors.Is(err, context.DeadlineExceeded):
		return "failed", fmt.Sprintf("%s excedeu o tempo limite de %s", what, j.e.Cfg.Build.Timeout.D())
	}
	return "failed", fmt.Sprintf("%s falhou: %v", what, err)
}

func (j *job) runBuild(ctx context.Context) (string, string) {
	e, app, d := j.e, j.app, j.d
	if d.GitSHA == "" {
		return "failed", "deploy sem commit"
	}
	j.stage("fetch")
	subject, _ := e.Git.Subject(ctx, e.RepoPath(app.Name), d.GitSHA)
	relDir := j.newReleaseDir(d.GitSHA)
	j.rel = store.Release{AppID: app.ID, GitSHA: d.GitSHA, GitRef: d.GitRef, Message: subject, Status: "building", Path: relDir}
	if err := e.Store.InsertRelease(ctx, &j.rel); err != nil {
		return "failed", err.Error()
	}
	e.Store.SetDeployRelease(ctx, d.ID, j.rel.ID)
	j.say("commit %s %s", short(d.GitSHA), subject)
	if err := e.Git.Archive(ctx, e.RepoPath(app.Name), d.GitSHA, relDir); err != nil {
		return j.failBefore(ctx, err, "obter o código")
	}

	j.stage("detect")
	plan, err := build.MakePlan(app, relDir, build.Options{CacheDir: e.Cfg.Dir("cache"), Rebuild: d.Rebuild})
	if err != nil {
		return j.failBefore(ctx, err, "detecção do runtime")
	}
	if plan.Runtime == "static" || plan.StaticDir != "" {
		clean, err := cleanStaticDir(plan.StaticDir)
		if err != nil {
			return j.failBefore(ctx, err, "static_dir")
		}
		plan.StaticDir = clean
	}
	j.say("runtime: %s", plan.Runtime)
	for _, w := range plan.Warnings {
		j.say("aviso: %s", w)
	}
	j.rel.Runtime = plan.Runtime
	e.Store.UpdateRelease(ctx, j.rel)
	if err := plan.Save(relDir); err != nil {
		return j.failBefore(ctx, err, "gravar o plano da release")
	}

	userEnv, err := e.Apps.UserEnv(ctx, app)
	if err != nil {
		return j.failBefore(ctx, err, "ler variáveis de ambiente")
	}
	bctx, cancelT := context.WithTimeout(ctx, e.Cfg.Build.Timeout.D())
	defer cancelT()
	for _, st := range plan.Steps {
		j.stage(st.Stage)
		j.say("$ %s", st.Label)
		if err := e.Runner.Run(bctx, relDir, mergeEnv(os.Environ(), envMap(userEnv), st.Env), j.out, st.Argv[0], st.Argv[1:]...); err != nil {
			if ctx.Err() == nil && bctx.Err() != nil {
				err = bctx.Err()
			}
			return j.failBefore(ctx, err, "`"+st.Label+"`")
		}
	}

	j.stage("prepare")
	if err := j.prepare(ctx, plan, relDir); err != nil {
		return j.failBefore(ctx, err, "preparação")
	}
	if ctx.Err() != nil {
		return j.failBefore(ctx, ctx.Err(), "deploy")
	}
	return j.activate(ctx, plan)
}

func (j *job) runRollback(ctx context.Context) (string, string) {
	e := j.e
	if j.d.ReleaseID == nil {
		return "failed", "rollback sem release alvo"
	}
	rel, err := e.Store.Release(ctx, *j.d.ReleaseID)
	if err != nil || rel.Status != "ready" {
		return "failed", "release alvo indisponível"
	}
	j.rel = rel
	j.say("rollback para a release %s (%s %s)", filepath.Base(rel.Path), short(rel.GitSHA), rel.Message)
	plan, err := build.LoadSaved(rel.Path)
	if err != nil {
		return "failed", err.Error()
	}
	j.stage("prepare")
	if err := j.prepare(ctx, plan, rel.Path); err != nil {
		return "failed", "preparação falhou: " + err.Error()
	}
	return j.activate(ctx, plan)
}

// prepare valida o estático ou grava start.sh e o envdir (§9.3 etapa 7).
func (j *job) prepare(ctx context.Context, plan build.Plan, relDir string) error {
	if plan.Runtime == "static" {
		dir := filepath.Join(relDir, plan.StaticDir)
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			return fmt.Errorf("o diretório estático %q não existe na release", plan.StaticDir)
		}
		if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
			j.say("aviso: %q não tem index.html", plan.StaticDir)
		}
		return nil
	}
	if err := j.writeStart(plan); err != nil {
		return err
	}
	return j.e.Apps.MaterializeEnv(ctx, j.app, filepath.Base(relDir), plan.Runtime)
}

func (j *job) writeStart(plan build.Plan) error {
	path := filepath.Join(j.e.Apps.AppDir(j.app.Name), "start.sh")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(runit.StartScript(j.e.Apps.PrefixDir(), plan.Start, plan.Venv)), 0o700); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (j *job) healthTimeout(plan build.Plan) time.Duration {
	t := time.Duration(j.app.HealthTimeoutS) * time.Second
	if plan.HealthTimeout > 0 && t == j.e.Cfg.Deploy.HealthTimeout.D() { // o app não foi editado: vale o droidpaas.toml
		t = plan.HealthTimeout
	}
	return t
}

func (j *job) waitHealthy(ctx context.Context, plan build.Plan) error {
	if j.app.Port == nil {
		return nil
	}
	timeout, interval := j.healthTimeout(plan), j.e.Cfg.Deploy.HealthInterval.D()
	if interval <= 0 {
		interval = time.Second
	}
	deadline := time.Now().Add(timeout)
	what := "TCP"
	if plan.HealthPath != "" {
		what = "GET " + plan.HealthPath
	}
	j.say("aguardando o health check (%s, até %s)…", what, timeout)
	var last error
	for {
		if last = j.e.Health(ctx, *j.app.Port, plan.HealthPath); last == nil {
			j.say("health check ok")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health check falhou após %s: %v", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// activate cruza o ponto sem volta: troca de release, start, health e finalização (ou rollback).
func (j *job) activate(parent context.Context, plan build.Plan) (string, string) {
	ctx := context.WithoutCancel(parent)
	e, app := j.e, j.app
	e.mu.Lock()
	j.cs.switched = true
	e.mu.Unlock()

	j.stage("switch")
	current := filepath.Join(e.Apps.AppDir(app.Name), "current")
	if err := atomicLink(current, j.rel.Path); err != nil {
		j.discardRelease()
		return "failed", "troca da release falhou: " + err.Error()
	}
	if plan.Runtime == "static" {
		if j.prev != nil && j.prev.Runtime != "static" { // o processo da release anterior não serve mais
			if err := e.Runit.Down(ctx, e.Apps.ServiceName(app.Name)); err != nil && !errors.Is(err, runit.ErrNoService) {
				j.say("aviso: não consegui parar o processo anterior: %v", err)
			}
		}
	} else if app.DesiredState == "running" {
		j.stage("start")
		e.Store.SetCrashed(ctx, app.ID, nil)
		if err := e.Runit.Ensure(ctx, e.Apps.Spec(app, false)); err != nil {
			return j.rollback(ctx, fmt.Errorf("preparar o serviço: %w", err))
		}
		if err := e.Runit.Restart(ctx, e.Apps.ServiceName(app.Name), 10*time.Second); err != nil {
			return j.rollback(ctx, fmt.Errorf("iniciar o app: %w", err))
		}
		j.stage("health")
		if err := j.waitHealthy(ctx, plan); err != nil {
			return j.rollback(ctx, err)
		}
	} else {
		j.say("o app está parado: a release foi ativada sem iniciar o processo")
	}

	e.Apps.Touch(app.ID) // o restart do deploy não é uma queda do app
	j.stage("finalize")
	size := dirSize(j.rel.Path)
	j.rel.Status, j.rel.SizeBytes = "ready", &size
	e.Store.UpdateRelease(ctx, j.rel)
	e.Store.SetCurrentRelease(ctx, app.ID, &j.rel.ID)
	e.Store.SetCrashed(ctx, app.ID, nil)
	e.prune(ctx, app, j.rel.ID, j.say)
	j.say("release %s ativa", filepath.Base(j.rel.Path))
	return "succeeded", ""
}

// rollback restaura a release anterior depois de uma falha em start/health (§9.3).
func (j *job) rollback(ctx context.Context, cause error) (string, string) {
	e, app := j.e, j.app
	j.say("ERRO: %v", cause)
	j.stage("rollback")
	msg := cause.Error()
	current := filepath.Join(e.Apps.AppDir(app.Name), "current")
	j.discardRelease() // no rollback manual a release alvo continua `ready`

	if j.prev == nil {
		e.Runit.Down(ctx, e.Apps.ServiceName(app.Name))
		os.Remove(current)
		j.say("não havia release anterior: o app ficou parado")
		return "failed", msg
	}
	prevPlan, err := build.LoadSaved(j.prev.Path)
	if err == nil {
		err = atomicLink(current, j.prev.Path)
	}
	if err == nil && prevPlan.Runtime == "static" {
		e.Runit.Down(ctx, e.Apps.ServiceName(app.Name))
	} else if err == nil {
		if err = j.writeStart(prevPlan); err == nil {
			err = e.Apps.MaterializeEnv(ctx, app, filepath.Base(j.prev.Path), prevPlan.Runtime)
		}
		if err == nil {
			err = e.Runit.Restart(ctx, e.Apps.ServiceName(app.Name), 10*time.Second)
		}
		if err == nil {
			err = j.waitHealthy(ctx, prevPlan)
		}
	}
	e.Apps.Touch(app.ID)
	if err != nil {
		j.say("o rollback também falhou: %v", err)
		e.Events.Error(ctx, "deploy.rollback_failed", &app.ID, "o rollback falhou", map[string]any{"err": err.Error()})
		return "failed", msg + "; o rollback também falhou: " + err.Error()
	}
	j.say("release anterior %s restaurada", filepath.Base(j.prev.Path))
	return "rolled_back", msg
}

// prune aplica a retenção (§8.3.4): mantém as N releases ready mais novas + a atual e remove o resto.
func (e *Engine) prune(ctx context.Context, a store.App, current int64, say func(string, ...any)) {
	keep := e.Cfg.Deploy.KeepReleases
	if v, ok, _ := e.Store.Setting(ctx, "keep_releases"); ok {
		if n := atoiOr(v, 0); n > 0 {
			keep = n
		}
	}
	rels, err := e.Store.Releases(ctx, a.ID, "ready")
	if err != nil {
		return
	}
	root := filepath.Join(e.Apps.AppDir(a.Name), "releases") + string(filepath.Separator)
	for i, r := range rels {
		if i < keep || r.ID == current {
			continue
		}
		if strings.HasPrefix(r.Path, root) {
			os.RemoveAll(r.Path)
		}
		r.Status = "pruned"
		e.Store.UpdateRelease(ctx, r)
		say("release antiga removida: %s", filepath.Base(r.Path))
	}
}

// ---- utilidades ----

func (j *job) newReleaseDir(sha string) string {
	base := fmt.Sprintf("%d-%s", j.e.Now().Unix(), short(sha))
	dir := filepath.Join(j.e.Apps.AppDir(j.app.Name), "releases", base)
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			return dir
		}
		dir = filepath.Join(j.e.Apps.AppDir(j.app.Name), "releases", fmt.Sprintf("%s-%d", base, i))
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func cleanStaticDir(d string) (string, error) {
	d = strings.TrimRight(d, "/")
	if strings.HasPrefix(d, "/") || strings.Contains(d, "..") {
		return "", fmt.Errorf("static_dir deve ser um caminho relativo dentro do repositório: %q", d)
	}
	return d, nil
}

func atomicLink(link, target string) error {
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

func dirSize(root string) int64 {
	var n int64
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func atoiOr(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if s == "" {
		return def
	}
	return n
}

func envMap(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// mergeEnv aplica as listas em ordem; chaves repetidas ficam com o último valor.
func mergeEnv(base []string, layers ...[]string) []string {
	idx := map[string]int{}
	out := make([]string, 0, len(base)+16)
	for _, l := range append([][]string{base}, layers...) {
		for _, kv := range l {
			k, _, _ := strings.Cut(kv, "=")
			if i, ok := idx[k]; ok {
				out[i] = kv
			} else {
				idx[k] = len(out)
				out = append(out, kv)
			}
		}
	}
	return out
}
