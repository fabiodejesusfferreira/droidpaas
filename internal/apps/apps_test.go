package apps

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

type fixture struct {
	svc *Service
	rt  *runit.Fake
	ctx context.Context
}

func newSvc(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypt.New(bytes.Repeat([]byte{3}, 32))
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	cfg.Ports.RangeStart, cfg.Ports.RangeEnd = 21000, 21009
	rt := runit.NewFake()
	s := &Service{Store: st, Runit: rt, Box: box, Cfg: cfg, Prefix: "/p",
		Events:   &events.Log{DB: st.DB, Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
		PortFree: func(int) bool { return true }}
	return &fixture{s, rt, ctx}
}

func (f *fixture) mustCreate(t *testing.T, name string) store.App {
	t.Helper()
	a, err := f.svc.Create(f.ctx, CreateInput{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fixture) giveRelease(t *testing.T, a store.App) {
	t.Helper()
	r := store.Release{AppID: a.ID, GitSHA: "abc", GitRef: "main", Status: "ready", Path: filepath.Join(f.svc.AppDir(a.Name), "releases", "1-abc"), Runtime: "node"}
	if err := f.svc.Store.InsertRelease(f.ctx, &r); err != nil {
		t.Fatal(err)
	}
	f.svc.Store.SetCurrentRelease(f.ctx, a.ID, &r.ID)
}

func TestCreateValidation(t *testing.T) {
	f := newSvc(t)
	for _, in := range []CreateInput{
		{Name: "A"}, {Name: "1app"}, {Name: "a"}, {Name: "app_x"}, {Name: strings.Repeat("a", 32)},
		{Name: "admin"}, {Name: "ok", Runtime: "ruby"}, {Name: "ok", Branch: "-x"}, {Name: "ok", Branch: "a..b"},
	} {
		var ve *ValidationError
		if _, err := f.svc.Create(f.ctx, in); !errors.As(err, &ve) {
			t.Errorf("%+v: esperava ValidationError, veio %v", in, err)
		}
	}
	if len(f.rt.Svcs) != 0 {
		t.Fatal("nada deve ser criado em caso de validação inválida")
	}
}

func TestCreateProvisionsEverything(t *testing.T) {
	f := newSvc(t)
	a := f.mustCreate(t, "minha-api")
	if a.Port == nil || *a.Port != 21000 || a.Runtime != "auto" || a.Branch != "main" || a.DesiredState != "running" {
		t.Fatalf("%+v", a)
	}
	for _, d := range []string{"env", "shared", "releases"} {
		if st, err := os.Stat(filepath.Join(f.svc.AppDir("minha-api"), d)); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, err, st)
		}
	}
	svc := f.rt.Svcs["dp-app-minha-api"]
	if svc == nil || !svc.Spec.Down || !strings.Contains(svc.Spec.Run, "chpst -e") {
		t.Fatalf("serviço runit: %+v", svc)
	}
	if _, err := f.svc.Create(f.ctx, CreateInput{Name: "minha-api"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicado: %v", err)
	}
	b := f.mustCreate(t, "outra")
	if *b.Port != 21001 {
		t.Fatalf("segunda porta = %d", *b.Port)
	}
}

func TestPortAllocationSkipsBusyAndExhausts(t *testing.T) {
	f := newSvc(t)
	f.svc.PortFree = func(p int) bool { return p != 21000 && p != 21001 }
	if a := f.mustCreate(t, "uma"); *a.Port != 21002 {
		t.Fatalf("porta = %d", *a.Port)
	}
	f.svc.PortFree = func(int) bool { return false }
	if _, err := f.svc.Create(f.ctx, CreateInput{Name: "duas"}); err == nil || !strings.Contains(err.Error(), "portas") {
		t.Fatalf("esgotadas: %v", err)
	}
	if _, err := f.svc.Store.AppByName(f.ctx, "duas"); err == nil {
		t.Fatal("app não deveria existir")
	}
}

type failingRunit struct{ *runit.Fake }

func (failingRunit) Ensure(context.Context, runit.ServiceSpec) error {
	return errors.New("runsvdir fora")
}

func TestCreateRollsBackOnFailure(t *testing.T) {
	f := newSvc(t)
	f.svc.Runit = failingRunit{f.rt}
	if _, err := f.svc.Create(f.ctx, CreateInput{Name: "falha"}); err == nil {
		t.Fatal("esperava erro")
	}
	if _, err := f.svc.Store.AppByName(f.ctx, "falha"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("registro do app deveria ter sido desfeito")
	}
	if _, err := os.Stat(f.svc.AppDir("falha")); err == nil {
		t.Fatal("diretório do app deveria ter sido removido")
	}
}

func TestLifecycle(t *testing.T) {
	f := newSvc(t)
	a := f.mustCreate(t, "app")
	if _, err := f.svc.Start(f.ctx, "app"); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("start sem release: %v", err)
	}
	if got := f.svc.State(f.ctx, a); got != "stopped" {
		t.Fatalf("estado inicial = %s", got)
	}
	f.giveRelease(t, a)
	if _, err := f.svc.Start(f.ctx, "app"); err != nil {
		t.Fatal(err)
	}
	a, _ = f.svc.Get(f.ctx, "app")
	if got := f.svc.State(f.ctx, a); got != "running" {
		t.Fatalf("depois do start = %s", got)
	}
	if _, err := f.svc.Restart(f.ctx, "app"); err != nil || f.rt.Svcs["dp-app-app"].Starts != 2 {
		t.Fatalf("restart: %v %+v", err, f.rt.Svcs["dp-app-app"])
	}
	a, _ = f.svc.Stop(f.ctx, "app")
	if a.DesiredState != "stopped" || f.svc.State(f.ctx, a) != "stopped" {
		t.Fatalf("stop: %+v", a)
	}
	// crash loop persistido e removido por ação explícita
	now := time.Now().UnixMilli()
	f.svc.Store.SetCrashed(f.ctx, a.ID, &now)
	a, _ = f.svc.Get(f.ctx, "app")
	if f.svc.State(f.ctx, a) != "crashed" {
		t.Fatal("deveria estar crashed")
	}
	a, _ = f.svc.Start(f.ctx, "app")
	if a.CrashedAt != nil {
		t.Fatal("Start deve limpar o crashed")
	}
	f.svc.Deploying = func(int64) bool { return true }
	if f.svc.State(f.ctx, a) != "deploying" {
		t.Fatal("deveria estar deploying")
	}
}

func TestUpdate(t *testing.T) {
	f := newSvc(t)
	f.mustCreate(t, "app")
	s := func(v string) *string { return &v }
	n := func(v int) *int { return &v }
	bad := []Patch{
		{Runtime: s("ruby")}, {Branch: s("--x")}, {HealthPath: s("health")}, {StaticDir: s("../x")}, {StaticDir: s("/etc")},
		{Nice: n(25)}, {HealthTimeoutS: n(0)}, {BuildCmd: s("a\nb")},
	}
	for _, p := range bad {
		var ve *ValidationError
		if _, err := f.svc.Update(f.ctx, "app", p); !errors.As(err, &ve) {
			t.Errorf("%+v: %v", p, err)
		}
	}
	got, err := f.svc.Update(f.ctx, "app", Patch{Runtime: s("node"), BuildCmd: s("npm run build"), HealthPath: s("/health"), StaticDir: s("dist/"), Nice: n(5), Branch: s("release/1.0")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "node" || *got.BuildCmd != "npm run build" || *got.HealthPath != "/health" || *got.StaticDir != "dist" || got.Nice != 5 || got.Branch != "release/1.0" {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(f.rt.Svcs["dp-app-app"].Spec.Run, "-n 5 ") {
		t.Fatal("nice novo deveria ir para o script run")
	}
	got, _ = f.svc.Update(f.ctx, "app", Patch{BuildCmd: s("")}) // limpar
	if got.BuildCmd != nil {
		t.Fatal("string vazia deveria limpar o campo")
	}
	if _, err := f.svc.Update(f.ctx, "nada", Patch{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestDelete(t *testing.T) {
	f := newSvc(t)
	a := f.mustCreate(t, "app")
	var ve *ValidationError
	if err := f.svc.Delete(f.ctx, "app", "outro"); !errors.As(err, &ve) {
		t.Fatalf("sem confirmação: %v", err)
	}
	if err := f.svc.Delete(f.ctx, "app", "app"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.rt.Svcs["dp-app-app"]; ok {
		t.Fatal("serviço runit não removido")
	}
	if _, err := os.Stat(f.svc.AppDir("app")); err == nil {
		t.Fatal("diretório não removido")
	}
	if _, err := f.svc.Store.AppByID(f.ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("registro não removido")
	}
}

func TestEnvMaskingValidationAndMaterialize(t *testing.T) {
	f := newSvc(t)
	a := f.mustCreate(t, "app")
	err := f.svc.EnvApply(f.ctx, "app", map[string]EnvValue{
		"DATABASE_URL": {"postgres://u:p@h/db", true},
		"LOG_LEVEL":    {"info", false},
		"MULTI":        {"linha1\nlinha2", false},
		"VAZIA":        {"", false},
		"ASPAS":        {`a "b" 'c' $d =e`, false},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := f.svc.EnvList(f.ctx, "app")
	byKey := map[string]EnvEntry{}
	for _, e := range list {
		byKey[e.Key] = e
	}
	if byKey["DATABASE_URL"].Value != Masked || !byKey["DATABASE_URL"].Secret || byKey["LOG_LEVEL"].Value != "info" {
		t.Fatalf("máscara: %+v", byKey)
	}
	if v, _ := f.svc.EnvReveal(f.ctx, "app", "DATABASE_URL"); v != "postgres://u:p@h/db" {
		t.Fatal(v)
	}
	if _, err := f.svc.EnvReveal(f.ctx, "app", "NAO"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	// o banco guarda cifrado
	vars, _ := f.svc.Store.Env(f.ctx, a.ID)
	for _, v := range vars {
		if strings.Contains(string(v.ValueEnc), "postgres") {
			t.Fatal("valor em claro no banco")
		}
	}
	// envdir
	dir := filepath.Join(f.svc.AppDir("app"), "env")
	read := func(k string) string {
		b, err := os.ReadFile(filepath.Join(dir, k))
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		return string(b)
	}
	if read("LOG_LEVEL") != "info" || read("MULTI") != "linha1\x00linha2" || read("VAZIA") != "\n" || read("ASPAS") != `a "b" 'c' $d =e` {
		t.Fatal("conteúdo do envdir incorreto")
	}
	if read("PORT") != "21000" || read("HOST") != "127.0.0.1" || read("DP_APP") != "app" || read("DP_RELEASE") != "\n" { // vazio vira "\n" (arquivo vazio apagaria a variável)
		t.Fatal("padrões injetados incorretos")
	}
	if st, _ := os.Stat(filepath.Join(dir, "DATABASE_URL")); st.Mode().Perm() != 0o600 {
		t.Fatalf("modo %v", st.Mode())
	}
	// remover variável apaga o arquivo; release atual aparece em DP_RELEASE e NODE_ENV só em node
	f.giveRelease(t, a)
	f.svc.EnvApply(f.ctx, "app", nil, []string{"LOG_LEVEL"})
	if _, err := os.Stat(filepath.Join(dir, "LOG_LEVEL")); err == nil {
		t.Fatal("arquivo órfão não removido")
	}
	if read("DP_RELEASE") != "1-abc" || read("NODE_ENV") != "production" {
		t.Fatalf("release/runtime: %q %q", read("DP_RELEASE"), read("NODE_ENV"))
	}
	if ents, _ := os.ReadDir(dir); func() bool {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".") {
				return true
			}
		}
		return false
	}() {
		t.Fatal("arquivo temporário sobrou")
	}
}

func TestEnvRejectsReservedAndInvalid(t *testing.T) {
	f := newSvc(t)
	f.mustCreate(t, "app")
	for k, v := range map[string]string{"PORT": "1", "HOST": "x", "DP_APP": "x", "NODE_ENV": "x", "1ABC": "x", "A-B": "x", "": "x", "OK": "a\x00b"} {
		var ve *ValidationError
		if err := f.svc.EnvApply(f.ctx, "app", map[string]EnvValue{k: {v, false}}, nil); !errors.As(err, &ve) {
			t.Errorf("%q: %v", k, err)
		}
	}
}

func TestDomains(t *testing.T) {
	f := newSvc(t)
	a := f.mustCreate(t, "app")
	if ds, _ := f.svc.Store.Domains(f.ctx, a.ID); len(ds) != 0 {
		t.Fatal("sem domínio base não há domínio automático")
	}
	f.svc.Store.SetSetting(f.ctx, "base_domain", "apps.exemplo.com")
	if err := f.svc.SyncAutoDomains(f.ctx); err != nil {
		t.Fatal(err)
	}
	ds, _ := f.svc.Store.Domains(f.ctx, a.ID)
	if len(ds) != 1 || ds[0].Hostname != "app.apps.exemplo.com" || !ds[0].Auto {
		t.Fatalf("%+v", ds)
	}
	var ve *ValidationError
	if err := f.svc.RemoveDomain(f.ctx, "app", "app.apps.exemplo.com"); !errors.As(err, &ve) {
		t.Fatalf("domínio automático removível: %v", err)
	}
	if err := f.svc.AddDomain(f.ctx, "app", "Meu.Site.com.br"); err != nil {
		t.Fatal(err)
	}
	b := f.mustCreate(t, "outra")
	_ = b
	if err := f.svc.AddDomain(f.ctx, "outra", "meu.site.com.br"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicado: %v", err)
	}
	for _, h := range []string{"semponto", "-x.com", "a b.com", "x.c"} {
		if err := f.svc.AddDomain(f.ctx, "app", h); !errors.As(err, &ve) {
			t.Errorf("%q: %v", h, err)
		}
	}
	// trocar o domínio base troca o automático e preserva o manual
	f.svc.Store.SetSetting(f.ctx, "base_domain", "novo.dev")
	f.svc.SyncAutoDomains(f.ctx)
	ds, _ = f.svc.Store.Domains(f.ctx, a.ID)
	names := []string{}
	for _, d := range ds {
		names = append(names, d.Hostname)
	}
	if strings.Join(names, ",") != "app.novo.dev,meu.site.com.br" {
		t.Fatal(names)
	}
	f.svc.Store.SetSetting(f.ctx, "base_domain", "")
	f.svc.SyncAutoDomains(f.ctx)
	if ds, _ = f.svc.Store.Domains(f.ctx, a.ID); len(ds) != 1 || ds[0].Auto {
		t.Fatalf("desativar domínio base: %+v", ds)
	}
}
