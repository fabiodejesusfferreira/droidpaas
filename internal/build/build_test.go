package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"droidpaas/internal/store"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range files {
		p := filepath.Join(d, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func app(rt string) store.App { return store.App{Name: "x", Runtime: rt} }
func sp(s string) *string     { return &s }

func argvs(p Plan) string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Stage+":"+strings.Join(s.Argv, " "))
	}
	return strings.Join(out, " | ")
}

func TestDetectOrder(t *testing.T) {
	for name, c := range map[string]struct {
		files map[string]string
		want  string
	}{
		"node vence python": {map[string]string{"package.json": "{}", "requirements.txt": ""}, "node"},
		"python":            {map[string]string{"requirements.txt": ""}, "python"},
		"pyproject":         {map[string]string{"pyproject.toml": ""}, "python"},
		"go":                {map[string]string{"go.mod": "module x"}, "go"},
		"static":            {map[string]string{"index.html": "x"}, "static"},
		"custom":            {map[string]string{"Procfile": "web: ./run"}, "custom"},
		"toml fixa":         {map[string]string{"package.json": "{}", "droidpaas.toml": `runtime = "custom"`, "Procfile": "web: x"}, "custom"},
	} {
		d := tree(t, c.files)
		rc, web, err := LoadRepoConfig(d)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Detect(d, "auto", rc, "", web)
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	d := tree(t, map[string]string{"README": "x"})
	if _, err := Detect(d, "auto", RepoConfig{}, "", ""); err != ErrNoRuntime {
		t.Fatalf("sem evidência: %v", err)
	}
	if got, _ := Detect(d, "node", RepoConfig{}, "", ""); got != "node" {
		t.Fatal("runtime fixado no app deve vencer a detecção")
	}
}

func TestRepoConfigStrict(t *testing.T) {
	d := tree(t, map[string]string{"droidpaas.toml": "runtime = \"node\"\nstart = \"node a.js\"\nhealth_timeout = \"45s\"\n"})
	rc, _, err := LoadRepoConfig(d)
	if err != nil || rc.Start != "node a.js" || rc.healthTimeout() != 45*time.Second {
		t.Fatalf("%+v %v", rc, err)
	}
	for body, why := range map[string]string{"runtim = \"x\"": "campo desconhecido", "health_timeout = \"abc\"": "duração inválida", "isto não é toml": "sintaxe"} {
		d := tree(t, map[string]string{"droidpaas.toml": body})
		if _, _, err := LoadRepoConfig(d); err == nil {
			t.Errorf("%s: esperava erro", why)
		}
	}
	d = tree(t, map[string]string{"Procfile": "worker: x\nweb:   gunicorn app:app  \n"})
	if _, web, _ := LoadRepoConfig(d); web != "gunicorn app:app" {
		t.Fatalf("Procfile: %q", web)
	}
}

func TestNodePlan(t *testing.T) {
	d := tree(t, map[string]string{"package.json": `{"main":"srv.js","scripts":{"build":"tsc","start":"node dist/s.js"}}`, "package-lock.json": "{}"})
	p, err := MakePlan(app("auto"), d, Options{CacheDir: "/c"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Runtime != "node" || argvs(p) != "install:npm ci | build:npm run build | build:npm prune --omit=dev" || p.Start != "npm start" {
		t.Fatalf("%s | %q", argvs(p), p.Start)
	}
	env := strings.Join(p.Steps[0].Env, " ")
	for _, want := range []string{"NODE_ENV=development", "--max-old-space-size=512", "npm_config_cache=/c/npm"} {
		if !strings.Contains(env, want) {
			t.Errorf("env sem %q: %s", want, env)
		}
	}
	// sem lockfile: avisa; start via main; build do app vence o script
	d = tree(t, map[string]string{"package.json": `{"main":"srv.js"}`})
	a := app("auto")
	a.BuildCmd = sp("make site")
	p, _ = MakePlan(a, d, Options{CacheDir: "/c"})
	if argvs(p) != "install:npm install | build:sh -c make site | build:npm prune --omit=dev" || p.Start != "node 'srv.js'" || len(p.Warnings) != 1 {
		t.Fatalf("%s | %q | %v", argvs(p), p.Start, p.Warnings)
	}
	// sem como iniciar
	d = tree(t, map[string]string{"package.json": `{}`})
	if _, err := MakePlan(app("auto"), d, Options{}); err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("esperava erro de start: %v", err)
	}
	// rebuild troca o cache do npm
	p, _ = MakePlan(a, tree(t, map[string]string{"package.json": `{"scripts":{"start":"x"}}`}), Options{CacheDir: "/c", Rebuild: true})
	if !strings.Contains(strings.Join(p.Steps[0].Env, " "), "npm-rebuild-") {
		t.Fatal("rebuild deve usar cache novo")
	}
}

func TestPrecedence(t *testing.T) {
	d := tree(t, map[string]string{"package.json": `{"scripts":{"start":"npm-start"}}`,
		"droidpaas.toml": "start = \"do-toml\"\nhealth_path = \"/toml\"\nstatic_dir = \"t\"\n", "Procfile": "web: do-procfile"})
	p, _ := MakePlan(app("auto"), d, Options{})
	if p.Start != "do-toml" || p.HealthPath != "/toml" || p.StaticDir != "t" {
		t.Fatalf("toml > Procfile > convenção: %+v", p)
	}
	a := app("auto")
	a.StartCmd, a.HealthPath = sp("do-app"), sp("/app")
	if p, _ = MakePlan(a, d, Options{}); p.Start != "do-app" || p.HealthPath != "/app" {
		t.Fatalf("app > toml: %+v", p)
	}
	d = tree(t, map[string]string{"package.json": `{"scripts":{"start":"npm-start"}}`, "Procfile": "web: do-procfile"})
	if p, _ = MakePlan(app("auto"), d, Options{}); p.Start != "do-procfile" {
		t.Fatalf("Procfile > convenção: %q", p.Start)
	}
}

func TestPythonPlan(t *testing.T) {
	d := tree(t, map[string]string{"requirements.txt": "flask", "Procfile": "web: gunicorn app:app"})
	p, err := MakePlan(app("auto"), d, Options{CacheDir: "/c"})
	if err != nil || !p.Venv || p.Start != "gunicorn app:app" ||
		argvs(p) != "install:python -m venv .venv | install:.venv/bin/pip install -r requirements.txt" {
		t.Fatalf("%v %s %+v", err, argvs(p), p)
	}
	d = tree(t, map[string]string{"pyproject.toml": "[project]"})
	if _, err := MakePlan(app("auto"), d, Options{}); err == nil || !strings.Contains(err.Error(), "start") {
		t.Fatalf("python sem start deve falhar: %v", err)
	}
}

func TestGoPlan(t *testing.T) {
	d := tree(t, map[string]string{"go.mod": "module x", "main.go": "package main"})
	p, err := MakePlan(app("auto"), d, Options{CacheDir: "/c"})
	if err != nil || argvs(p) != "build:go build -o app ." || p.Start != "./app" {
		t.Fatalf("%v %s %q", err, argvs(p), p.Start)
	}
	d = tree(t, map[string]string{"go.mod": "module x", "cmd/srv/main.go": "package main"})
	if p, _ = MakePlan(app("auto"), d, Options{}); argvs(p) != "build:go build -o app ./cmd/srv" {
		t.Fatal(argvs(p))
	}
	d = tree(t, map[string]string{"go.mod": "module x", "cmd/a/main.go": "", "cmd/b/main.go": ""})
	if _, err := MakePlan(app("auto"), d, Options{}); err == nil {
		t.Fatal("dois mains devem exigir build explícito")
	}
}

func TestStaticAndCustomPlans(t *testing.T) {
	d := tree(t, map[string]string{"index.html": "x"})
	p, err := MakePlan(app("auto"), d, Options{})
	if err != nil || p.Runtime != "static" || len(p.Steps) != 0 || p.Start != "" {
		t.Fatalf("%v %+v", err, p)
	}
	d = tree(t, map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`, "package-lock.json": "{}"})
	a := app("static")
	a.BuildCmd, a.StaticDir = sp("npm run build"), sp("dist")
	p, _ = MakePlan(a, d, Options{CacheDir: "/c"})
	if p.Runtime != "static" || argvs(p) != "install:npm ci | build:sh -c npm run build" || p.StaticDir != "dist" || p.Start != "" {
		t.Fatalf("%s %+v", argvs(p), p)
	}
	d = tree(t, map[string]string{"Procfile": "web: ./serve --port $PORT"})
	if p, _ = MakePlan(app("auto"), d, Options{}); p.Runtime != "custom" || p.Start != "./serve --port $PORT" {
		t.Fatalf("%+v", p)
	}
	if _, err := MakePlan(app("custom"), tree(t, map[string]string{"x": ""}), Options{}); err == nil {
		t.Fatal("custom sem start deve falhar")
	}
}

func TestSaveLoad(t *testing.T) {
	d := t.TempDir()
	in := Plan{Runtime: "python", Start: "gunicorn a:a", Venv: true, HealthPath: "/h", HealthTimeout: 45 * time.Second, StaticDir: "s"}
	if err := in.Save(d); err != nil {
		t.Fatal(err)
	}
	out, err := LoadSaved(d)
	if err != nil || out.Runtime != "python" || out.Start != "gunicorn a:a" || !out.Venv || out.HealthTimeout != 45*time.Second || out.StaticDir != "s" {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := LoadSaved(t.TempDir()); err == nil {
		t.Fatal("sem arquivo deve falhar")
	}
}
