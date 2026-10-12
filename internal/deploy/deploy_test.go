package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
	"droidpaas/internal/system"
)

type fx struct {
	t    *testing.T
	e    *Engine
	svc  *apps.Service
	rt   *runit.Fake
	work string
	ctx  context.Context
}

func newFx(t *testing.T) *fx {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypt.New(bytes.Repeat([]byte{5}, 32))
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	cfg.Ports.RangeStart, cfg.Ports.RangeEnd = 22000, 22050
	cfg.Deploy.HealthTimeout = config.Duration(time.Second)
	cfg.Deploy.HealthInterval = config.Duration(50 * time.Millisecond)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ev := &events.Log{DB: st.DB, Log: log}
	rt := runit.NewFake()
	svc := &apps.Service{Store: st, Runit: rt, Box: box, Events: ev, Cfg: cfg, Prefix: "/p", PortFree: func(int) bool { return true }}
	f := &fx{t: t, rt: rt, svc: svc, ctx: ctx, work: t.TempDir()}
	f.e = New(&Engine{Store: st, Apps: svc, Runit: rt, Git: system.GitCLI{}, Runner: system.ExecRunner{KillAfter: 500 * time.Millisecond},
		Cfg: cfg, Events: ev, Log: log,
		Health: func(context.Context, int, string) error {
			// a release "quebrada" é a que contém o arquivo `bad`
			if _, err := os.Stat(filepath.Join(svc.AppDir("web"), "current", "bad")); err == nil {
				return errors.New("sem resposta")
			}
			return nil
		}})
	svc.Deploying = f.e.IsDeploying
	go f.e.Run(ctx)
	return f
}

func (f *fx) git(args ...string) string {
	f.t.Helper()
	c := exec.Command("git", args...)
	c.Dir = f.work
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newApp cria o app e o repositório bare em apps/<nome>/repo.git.
func (f *fx) newApp(name string) store.App {
	f.t.Helper()
	a, err := f.svc.Create(f.ctx, apps.CreateInput{Name: name})
	if err != nil {
		f.t.Fatal(err)
	}
	repo := f.e.RepoPath(name)
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", repo).CombinedOutput(); err != nil {
		f.t.Fatalf("%v %s", err, out)
	}
	f.git("init", "-q", "-b", "main")
	f.git("remote", "add", "origin", repo)
	return a
}

// commit grava os arquivos, dá push e devolve o sha. Um valor vazio remove o arquivo.
func (f *fx) commit(files map[string]string) string {
	f.t.Helper()
	for name, body := range files {
		p := filepath.Join(f.work, name)
		if body == "" {
			os.Remove(p)
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o755)
	}
	f.git("add", "-A")
	f.git("commit", "-qm", "c"+time.Now().Format("150405.000000"), "--allow-empty")
	f.git("push", "-q", "origin", "main")
	return f.git("rev-parse", "HEAD")
}

func (f *fx) deploy(app string, trigger string, o EnqueueOpts) store.Deploy {
	f.t.Helper()
	d, err := f.e.Enqueue(f.ctx, app, trigger, o)
	if err != nil {
		f.t.Fatal(err)
	}
	return f.wait(d.ID)
}

func (f *fx) wait(id int64) store.Deploy {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		d, _ := f.e.Store.Deploy(f.ctx, id)
		if d.Status != "queued" && d.Status != "running" {
			return d
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("deploy %d não terminou", id)
	return store.Deploy{}
}

func (f *fx) log(d store.Deploy) string { b, _ := os.ReadFile(d.LogPath); return string(b) }

func (f *fx) current(app string) string {
	p, _ := os.Readlink(filepath.Join(f.svc.AppDir(app), "current"))
	return p
}

const procfileApp = "#!/bin/sh\necho ok\n"

func customFiles(extra map[string]string) map[string]string {
	m := map[string]string{"Procfile": "web: ./app.sh", "app.sh": procfileApp}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestStaticDeploy(t *testing.T) {
	f := newFx(t)
	f.newApp("blog")
	f.commit(map[string]string{"index.html": "<h1>oi</h1>", "dist/index.html": "x"})
	d := f.deploy("blog", "manual", EnqueueOpts{})
	if d.Status != "succeeded" {
		t.Fatalf("%s: %v\n%s", d.Status, d.Error, f.log(d))
	}
	a, _ := f.svc.Get(f.ctx, "blog")
	rel, _ := f.e.Store.Release(f.ctx, *a.CurrentReleaseID)
	if rel.Status != "ready" || rel.Runtime != "static" || rel.GitSHA == "" || rel.SizeBytes == nil {
		t.Fatalf("%+v", rel)
	}
	if f.current("blog") != rel.Path {
		t.Fatalf("symlink current = %q, release = %q", f.current("blog"), rel.Path)
	}
	if b, _ := os.ReadFile(filepath.Join(f.svc.AppDir("blog"), "current", "index.html")); string(b) != "<h1>oi</h1>" {
		t.Fatal("conteúdo da release")
	}
	if _, err := os.Stat(filepath.Join(rel.Path, ".git")); err == nil {
		t.Fatal("release não deve ter .git")
	}
	if f.rt.Svcs["dp-app-blog"].Starts != 0 {
		t.Fatal("site estático não tem processo")
	}
	f.commit(map[string]string{"dist/index.html": "novo"})
	a2, _ := f.svc.Get(f.ctx, "blog")
	_ = a2
}

func TestProcessDeployAndEnv(t *testing.T) {
	f := newFx(t)
	a := f.newApp("web")
	f.svc.EnvApply(f.ctx, "web", map[string]apps.EnvValue{"FOO": {Value: "bar"}}, nil)
	f.commit(customFiles(map[string]string{"droidpaas.toml": "build = \"echo FOO=$FOO > built.txt\"\nhealth_path = \"/health\"\n"}))
	d := f.deploy("web", "manual", EnqueueOpts{})
	if d.Status != "succeeded" {
		t.Fatalf("%s: %v\n%s", d.Status, d.Error, f.log(d))
	}
	cur := filepath.Join(f.svc.AppDir("web"), "current")
	if b, _ := os.ReadFile(filepath.Join(cur, "built.txt")); strings.TrimSpace(string(b)) != "FOO=bar" {
		t.Fatalf("o build deve enxergar as variáveis do app: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.svc.AppDir("web"), "start.sh")); !strings.HasSuffix(string(b), "exec ./app.sh\n") {
		t.Fatalf("start.sh: %q", b)
	}
	if st, _ := os.Stat(filepath.Join(f.svc.AppDir("web"), "start.sh")); st.Mode().Perm()&0o700 != 0o700 {
		t.Fatalf("modo do start.sh %v", st.Mode())
	}
	envd := filepath.Join(f.svc.AppDir("web"), "env")
	rel := filepath.Base(f.current("web"))
	if b, _ := os.ReadFile(filepath.Join(envd, "DP_RELEASE")); string(b) != rel {
		t.Fatalf("DP_RELEASE = %q, esperado %q", b, rel)
	}
	if b, _ := os.ReadFile(filepath.Join(envd, "PORT")); string(b) != "22000" {
		t.Fatalf("PORT = %q", b)
	}
	if s := f.rt.Svcs["dp-app-web"]; s == nil || !s.Up || s.Starts != 1 {
		t.Fatalf("serviço: %+v", s)
	}
	cur2, _ := f.svc.Get(f.ctx, "web")
	if cur2.CurrentReleaseID == nil || a.ID != cur2.ID || f.svc.State(f.ctx, cur2) != "running" && f.svc.State(f.ctx, cur2) != "starting" {
		t.Fatalf("estado: %+v", cur2)
	}
	// o log do deploy tem os estágios na ordem
	lg := f.log(d)
	last := -1
	for _, st := range []string{"-----> fetch", "-----> detect", "-----> build", "-----> prepare", "-----> switch", "-----> start", "-----> health", "-----> finalize"} {
		i := strings.Index(lg, st)
		if i < 0 || i < last {
			t.Fatalf("estágio %q fora de ordem/ausente:\n%s", st, lg)
		}
		last = i
	}
}

func TestBuildFailureKeepsRunningRelease(t *testing.T) { // RNF-09 / AT-04
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(nil))
	d1 := f.deploy("web", "manual", EnqueueOpts{})
	if d1.Status != "succeeded" {
		t.Fatal(d1.Status, d1.Error)
	}
	before := f.current("web")
	startsBefore := f.rt.Svcs["dp-app-web"].Starts
	startSh, _ := os.ReadFile(filepath.Join(f.svc.AppDir("web"), "start.sh"))

	f.commit(customFiles(map[string]string{"droidpaas.toml": "build = \"echo quebrou; exit 3\"\n"}))
	d2 := f.deploy("web", "manual", EnqueueOpts{})
	if d2.Status != "failed" || d2.Error == nil || !strings.Contains(*d2.Error, "falhou") {
		t.Fatalf("%s %v", d2.Status, d2.Error)
	}
	if !strings.Contains(f.log(d2), "quebrou") {
		t.Fatal("a saída do build deve estar no log")
	}
	if f.current("web") != before {
		t.Fatal("o symlink current foi alterado por um deploy que falhou antes da troca")
	}
	if f.rt.Svcs["dp-app-web"].Starts != startsBefore {
		t.Fatal("o app em execução não pode ser reiniciado por falha de build")
	}
	if now, _ := os.ReadFile(filepath.Join(f.svc.AppDir("web"), "start.sh")); string(now) != string(startSh) {
		t.Fatal("start.sh alterado")
	}
	rels, _ := f.e.Store.Releases(f.ctx, 1, "")
	if len(rels) != 2 || rels[0].Status != "failed" {
		t.Fatalf("%+v", rels)
	}
	if _, err := os.Stat(rels[0].Path); err == nil {
		t.Fatal("diretório da release falha deveria ter sido removido")
	}
	a, _ := f.svc.Get(f.ctx, "web")
	if a.CurrentReleaseID == nil || *a.CurrentReleaseID != rels[1].ID {
		t.Fatal("release atual mudou")
	}
}

func TestHealthFailureRollsBack(t *testing.T) { // AT-05
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(nil))
	d1 := f.deploy("web", "manual", EnqueueOpts{})
	if d1.Status != "succeeded" {
		t.Fatal(d1.Status)
	}
	prev := f.current("web")
	f.commit(customFiles(map[string]string{"Procfile": "web: ./novo.sh", "novo.sh": "#!/bin/sh\n", "bad": "x"}))
	d2 := f.deploy("web", "manual", EnqueueOpts{})
	if d2.Status != "rolled_back" {
		t.Fatalf("%s %v\n%s", d2.Status, d2.Error, f.log(d2))
	}
	if f.current("web") != prev {
		t.Fatal("o symlink deveria voltar para a release anterior")
	}
	if b, _ := os.ReadFile(filepath.Join(f.svc.AppDir("web"), "start.sh")); !strings.Contains(string(b), "./app.sh") {
		t.Fatalf("start.sh deveria voltar ao da release anterior: %q", b)
	}
	a, _ := f.svc.Get(f.ctx, "web")
	if a.CurrentReleaseID == nil {
		t.Fatal("release atual perdida")
	}
	if cur, _ := f.e.Store.Release(f.ctx, *a.CurrentReleaseID); cur.Path != prev {
		t.Fatal("current_release_id não é a anterior")
	}
	rels, _ := f.e.Store.Releases(f.ctx, a.ID, "")
	if rels[0].Status != "failed" {
		t.Fatalf("a nova release deve ficar failed: %+v", rels[0])
	}
	if !strings.Contains(f.log(d2), "restaurada") {
		t.Fatal(f.log(d2))
	}
}

func TestHealthFailureWithoutPreviousStopsApp(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(map[string]string{"bad": "x"}))
	d := f.deploy("web", "manual", EnqueueOpts{})
	if d.Status != "failed" {
		t.Fatalf("%s %v", d.Status, d.Error)
	}
	if f.rt.Svcs["dp-app-web"].Up {
		t.Fatal("sem release anterior o app deve ficar parado")
	}
	if _, err := os.Lstat(filepath.Join(f.svc.AppDir("web"), "current")); err == nil {
		t.Fatal("o symlink current deveria ter sido removido")
	}
	a, _ := f.svc.Get(f.ctx, "web")
	if a.CurrentReleaseID != nil {
		t.Fatal("não pode haver release atual")
	}
}

func TestManualRollback(t *testing.T) { // AT-06
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(map[string]string{"v": "1"}))
	f.deploy("web", "manual", EnqueueOpts{})
	r1 := f.current("web")
	time.Sleep(1100 * time.Millisecond) // nomes de release usam segundos
	f.commit(customFiles(map[string]string{"v": "2", "Procfile": "web: ./dois.sh", "dois.sh": "#!/bin/sh\n"}))
	f.deploy("web", "manual", EnqueueOpts{})
	r2 := f.current("web")
	if r1 == r2 {
		t.Fatal("deveriam ser releases diferentes")
	}
	d := f.deploy("web", "rollback", EnqueueOpts{})
	if d.Status != "succeeded" {
		t.Fatalf("%s %v\n%s", d.Status, d.Error, f.log(d))
	}
	if f.current("web") != r1 {
		t.Fatalf("current = %q, esperado %q", f.current("web"), r1)
	}
	if b, _ := os.ReadFile(filepath.Join(f.svc.AppDir("web"), "start.sh")); !strings.Contains(string(b), "./app.sh") {
		t.Fatalf("start.sh do rollback: %q", b)
	}
	a, _ := f.svc.Get(f.ctx, "web")
	if cur, _ := f.e.Store.Release(f.ctx, *a.CurrentReleaseID); cur.Path != r1 || cur.Status != "ready" {
		t.Fatalf("%+v", cur)
	}
	if _, err := os.Stat(r2); err != nil {
		t.Fatal("a release mais nova não pode ser apagada pelo rollback")
	}
	// sem release anterior ao alvo atual → erro; alvo inválido → erro
	if _, err := f.e.Enqueue(f.ctx, "web", "rollback", EnqueueOpts{ReleaseID: 9999}); !errors.Is(err, ErrBadRelease) {
		t.Fatal(err)
	}
}

func TestRollbackWithoutPrevious(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(nil))
	f.deploy("web", "manual", EnqueueOpts{})
	if _, err := f.e.Enqueue(f.ctx, "web", "rollback", EnqueueOpts{}); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("%v", err)
	}
	if _, err := f.e.Enqueue(f.ctx, "web", "manual", EnqueueOpts{Ref: "nao-existe"}); !errors.Is(err, ErrBadRef) {
		t.Fatalf("%v", err)
	}
	if _, err := f.e.Enqueue(f.ctx, "web", "manual", EnqueueOpts{Ref: "--output=/tmp/x"}); !errors.Is(err, ErrBadRef) {
		t.Fatalf("ref com cara de opção: %v", err)
	}
}

func TestRetention(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.svc.Store.SetSetting(f.ctx, "keep_releases", "2")
	var paths []string
	for i := 0; i < 4; i++ {
		f.commit(customFiles(map[string]string{"n": string(rune('a' + i))}))
		d := f.deploy("web", "manual", EnqueueOpts{})
		if d.Status != "succeeded" {
			t.Fatal(d.Status, d.Error)
		}
		paths = append(paths, f.current("web"))
		time.Sleep(1100 * time.Millisecond)
	}
	for i, p := range paths {
		_, err := os.Stat(p)
		if want := i >= 2; (err == nil) != want {
			t.Errorf("release %d existe=%v, esperado %v", i, err == nil, want)
		}
	}
	a, _ := f.svc.Get(f.ctx, "web")
	rels, _ := f.e.Store.Releases(f.ctx, a.ID, "ready")
	if len(rels) != 2 {
		t.Fatalf("ready = %d", len(rels))
	}
}

func TestBuildTimeout(t *testing.T) {
	f := newFx(t)
	f.e.Cfg.Build.Timeout = config.Duration(400 * time.Millisecond)
	f.newApp("web")
	f.commit(customFiles(map[string]string{"droidpaas.toml": "build = \"sleep 30\"\n"}))
	start := time.Now()
	d := f.deploy("web", "manual", EnqueueOpts{})
	if d.Status != "failed" || !strings.Contains(*d.Error, "tempo limite") || time.Since(start) > 10*time.Second {
		t.Fatalf("%s %v (%v)", d.Status, d.Error, time.Since(start))
	}
}

func TestCancelRunningBuild(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(nil))
	f.deploy("web", "manual", EnqueueOpts{})
	prev := f.current("web")
	f.commit(customFiles(map[string]string{"droidpaas.toml": "build = \"echo comecou; sleep 30\"\n"}))
	d, _ := f.e.Enqueue(f.ctx, "web", "manual", EnqueueOpts{})
	for i := 0; i < 200; i++ { // espera o build começar
		if b, _ := os.ReadFile(d.LogPath); strings.Contains(string(b), "comecou") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := f.e.Cancel(f.ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	got := f.wait(d.ID)
	if got.Status != "cancelled" {
		t.Fatalf("%s %v", got.Status, got.Error)
	}
	if f.current("web") != prev {
		t.Fatal("cancelar não pode mexer na release ativa")
	}
	if err := f.e.Cancel(f.ctx, d.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("cancelar de novo: %v", err)
	}
}

func TestQueueSerializesPerApp(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(map[string]string{"droidpaas.toml": "build = \"sleep 0.3\"\n"}))
	d1, _ := f.e.Enqueue(f.ctx, "web", "manual", EnqueueOpts{})
	d2, _ := f.e.Enqueue(f.ctx, "web", "rebuild", EnqueueOpts{})
	// enquanto o primeiro roda, o segundo espera na fila
	time.Sleep(150 * time.Millisecond)
	if x, _ := f.e.Store.Deploy(f.ctx, d2.ID); x.Status != "queued" {
		t.Fatalf("segundo deploy deveria estar queued, está %s", x.Status)
	}
	a, b := f.wait(d1.ID), f.wait(d2.ID)
	if a.Status != "succeeded" || b.Status != "succeeded" {
		t.Fatalf("%s %s", a.Status, b.Status)
	}
	if *b.StartedAt < *a.FinishedAt {
		t.Fatal("o segundo deploy começou antes do primeiro terminar")
	}
	if !b.Rebuild {
		t.Fatal("trigger rebuild deve marcar rebuild")
	}
	// cancelar item da fila
	f.e.Cfg.Build.MaxConcurrent = 1
	d3, _ := f.e.Enqueue(f.ctx, "web", "manual", EnqueueOpts{})
	d4, _ := f.e.Enqueue(f.ctx, "web", "manual", EnqueueOpts{})
	if err := f.e.Cancel(f.ctx, d4.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(d3.ID)
	if x, _ := f.e.Store.Deploy(f.ctx, d4.ID); x.Status != "cancelled" {
		t.Fatal(x.Status)
	}
}

func TestStoppedAppIsNotStarted(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(nil))
	f.svc.Store.SetDesiredState(f.ctx, 1, "stopped")
	d := f.deploy("web", "manual", EnqueueOpts{})
	if d.Status != "succeeded" {
		t.Fatal(d.Status, d.Error)
	}
	if s := f.rt.Svcs["dp-app-web"]; s.Up || s.Starts != 0 {
		t.Fatalf("app parado não deve ser iniciado por um deploy: %+v", s)
	}
	if !strings.Contains(f.log(d), "parado") {
		t.Fatal(f.log(d))
	}
}

func TestDetectionFailureIsClear(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.commit(map[string]string{"README": "nada"})
	d := f.deploy("web", "manual", EnqueueOpts{})
	if d.Status != "failed" || !strings.Contains(*d.Error, "detectar o runtime") {
		t.Fatalf("%s %v", d.Status, d.Error)
	}
}

func TestFollowStreamsAndReplays(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	f.commit(customFiles(map[string]string{"droidpaas.toml": "build = \"echo l1; sleep 0.3; echo l2\"\n"}))
	d, _ := f.e.Enqueue(f.ctx, "web", "manual", EnqueueOpts{})
	var lines []string
	status, err := f.e.Follow(f.ctx, d.ID, 0, func(n int, l string) { lines = append(lines, l) })
	if err != nil || status != "succeeded" {
		t.Fatalf("%s %v", status, err)
	}
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "l1") || !strings.Contains(all, "l2") || !strings.Contains(all, "deploy #") {
		t.Fatalf("faltam linhas (%d):\n%s", len(lines), all)
	}
	// deploy já terminado: o replay vem do arquivo; `from` pula linhas já vistas
	var again []string
	f.e.Follow(f.ctx, d.ID, 3, func(n int, l string) { again = append(again, l); _ = n })
	if len(again) != len(lines)-3 || again[0] != lines[3] {
		t.Fatalf("retomada: %d linhas, esperado %d", len(again), len(lines)-3)
	}
}

func TestRecoverBuildingReleases(t *testing.T) {
	f := newFx(t)
	a := f.newApp("web")
	dir := filepath.Join(f.svc.AppDir("web"), "releases", "1-aaaaaaa")
	os.MkdirAll(dir, 0o755)
	r := store.Release{AppID: a.ID, GitSHA: "a", GitRef: "main", Status: "building", Path: dir}
	f.e.Store.InsertRelease(f.ctx, &r)
	f.e.Recover(f.ctx)
	got, _ := f.e.Store.Release(f.ctx, r.ID)
	if got.Status != "failed" {
		t.Fatal(got.Status)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("diretório parcial deveria ser removido")
	}
}

func TestGitPushTrigger(t *testing.T) {
	f := newFx(t)
	f.newApp("web")
	sha := f.commit(customFiles(nil))
	d := f.deploy("web", "git_push", EnqueueOpts{SHA: sha, Ref: "main"})
	if d.Status != "succeeded" || d.GitSHA != sha || d.Trigger != "git_push" {
		t.Fatalf("%+v", d)
	}
}
