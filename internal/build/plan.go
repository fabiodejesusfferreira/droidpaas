package build

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"droidpaas/internal/store"
)

// Step é um comando do pipeline. Argv é executado sem shell; comandos do admin viram ["sh","-c",cmd].
type Step struct {
	Stage string // install | build
	Label string // texto exibido no log
	Argv  []string
	Env   []string // KEY=VAL extras
}

type Plan struct {
	Runtime       string
	Steps         []Step
	Start         string // vazio para static
	Venv          bool
	HealthPath    string
	HealthTimeout time.Duration // 0 = usar o do app
	StaticDir     string
	Warnings      []string
}

type Options struct {
	CacheDir   string // $DP_HOME/cache
	Rebuild    bool   // ignora caches de dependências (§9.5)
	NodeHeapMB int    // --max-old-space-size durante o build (R-03)
}

func sh(cmd string) []string { return []string{"sh", "-c", cmd} }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// MakePlan resolve a configuração (precedência: campos do app > droidpaas.toml > Procfile > convenção)
// e monta os passos do pipeline do runtime.
func MakePlan(app store.App, dir string, o Options) (Plan, error) {
	rc, web, err := LoadRepoConfig(dir)
	if err != nil {
		return Plan{}, err
	}
	staticDir := first(deref(app.StaticDir), rc.StaticDir)
	rt, err := Detect(dir, app.Runtime, rc, staticDir, web)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Runtime: rt, StaticDir: staticDir, HealthPath: first(deref(app.HealthPath), rc.HealthPath), HealthTimeout: rc.healthTimeout()}
	buildCmd := first(deref(app.BuildCmd), rc.Build)
	startCmd := first(deref(app.StartCmd), rc.Start, web)
	if o.NodeHeapMB == 0 {
		o.NodeHeapMB = 512
	}
	nodeEnv := []string{
		"NODE_ENV=development", // devDependencies são necessárias para o build; a poda vem depois
		fmt.Sprintf("NODE_OPTIONS=--max-old-space-size=%d", o.NodeHeapMB),
		"npm_config_cache=" + filepath.Join(o.CacheDir, "npm"),
		"npm_config_update_notifier=false", "npm_config_fund=false", "npm_config_audit=false",
	}
	if o.Rebuild {
		nodeEnv[2] = "npm_config_cache=" + filepath.Join(o.CacheDir, "npm-rebuild-"+fmt.Sprint(time.Now().UnixNano()))
	}

	nodeInstall := func() Step {
		if exists(dir, "package-lock.json") || exists(dir, "npm-shrinkwrap.json") {
			return Step{"install", "npm ci", []string{"npm", "ci"}, nodeEnv}
		}
		p.Warnings = append(p.Warnings, "sem package-lock.json: usando `npm install`, as versões podem variar entre deploys")
		return Step{"install", "npm install", []string{"npm", "install"}, nodeEnv}
	}

	switch rt {
	case "node":
		p.Steps = append(p.Steps, nodeInstall())
		pkg, perr := readPackageJSON(dir)
		if perr != nil {
			return p, perr
		}
		switch {
		case buildCmd != "":
			p.Steps = append(p.Steps, Step{"build", buildCmd, sh(buildCmd), nodeEnv})
		case pkg.Scripts["build"] != "":
			p.Steps = append(p.Steps, Step{"build", "npm run build", []string{"npm", "run", "build"}, nodeEnv})
		}
		if exists(dir, "node_modules") || len(p.Steps) > 0 {
			p.Steps = append(p.Steps, Step{"build", "npm prune --omit=dev", []string{"npm", "prune", "--omit=dev"}, nodeEnv})
		}
		switch {
		case startCmd != "":
			p.Start = startCmd
		case pkg.Scripts["start"] != "":
			p.Start = nodeStart(pkg.Scripts["start"])
		case pkg.Main != "":
			p.Start = "node " + shellQuote(pkg.Main)
		case exists(dir, "index.js"):
			p.Start = "node index.js"
		default:
			return p, errors.New("não sei iniciar o app node: defina o script `start` no package.json, `start` no droidpaas.toml, um Procfile `web:` ou o comando de start do app")
		}
	case "python":
		pipEnv := []string{"PIP_CACHE_DIR=" + filepath.Join(o.CacheDir, "pip"), "PIP_DISABLE_PIP_VERSION_CHECK=1"}
		if o.Rebuild {
			pipEnv = append(pipEnv, "PIP_NO_CACHE_DIR=1")
		}
		p.Venv = true
		p.Steps = append(p.Steps, Step{"install", "python -m venv .venv", []string{"python", "-m", "venv", ".venv"}, pipEnv})
		switch {
		case exists(dir, "requirements.txt"):
			p.Steps = append(p.Steps, Step{"install", "pip install -r requirements.txt", []string{".venv/bin/pip", "install", "-r", "requirements.txt"}, pipEnv})
		case exists(dir, "pyproject.toml"):
			p.Steps = append(p.Steps, Step{"install", "pip install .", []string{".venv/bin/pip", "install", "."}, pipEnv})
		}
		if buildCmd != "" {
			p.Steps = append(p.Steps, Step{"build", buildCmd, sh(buildCmd), pipEnv})
		}
		if startCmd == "" {
			return p, errors.New("o runtime python exige o comando de start (não há convenção segura): defina um Procfile `web:`, `start` no droidpaas.toml ou o comando de start do app")
		}
		p.Start = startCmd
		p.Warnings = append(p.Warnings, "dependências nativas sem wheel para Android costumam falhar ou demorar muito (compilam do código-fonte)")
	case "go":
		goEnv := []string{"GOCACHE=" + filepath.Join(o.CacheDir, "go-build"), "GOPATH=" + filepath.Join(o.CacheDir, "go")}
		p.Warnings = append(p.Warnings, "go build no aparelho consome bastante RAM")
		if buildCmd != "" {
			p.Steps = append(p.Steps, Step{"build", buildCmd, sh(buildCmd), goEnv})
		} else {
			target, terr := goTarget(dir)
			if terr != nil {
				return p, terr
			}
			argv := []string{"go", "build", "-o", "app"}
			if o.Rebuild {
				argv = append(argv, "-a")
			}
			p.Steps = append(p.Steps, Step{"build", "go build -o app " + target, append(argv, target), goEnv})
		}
		p.Start = first(startCmd, "./app")
	case "static":
		if buildCmd != "" {
			if exists(dir, "package.json") {
				p.Steps = append(p.Steps, nodeInstall())
			}
			p.Steps = append(p.Steps, Step{"build", buildCmd, sh(buildCmd), nodeEnv})
		}
	case "custom":
		if buildCmd != "" {
			p.Steps = append(p.Steps, Step{"build", buildCmd, sh(buildCmd), nil})
		}
		if startCmd == "" {
			return p, errors.New("o runtime custom exige um comando de start: Procfile `web:`, `start` no droidpaas.toml ou o comando de start do app")
		}
		p.Start = startCmd
	default:
		return p, fmt.Errorf("runtime desconhecido: %q", rt)
	}
	return p, nil
}

// simpleNodeRe reconhece scripts como "node server.js --flag": sem pipes, &&, variáveis nem aspas.
var simpleNodeRe = regexp.MustCompile(`^node(\s+[A-Za-z0-9_./@:=-]+)+$`)

// nodeStart evita `npm start` quando o script é só `node …`: o npm vira um processo intermediário (dezenas de MB de RAM) e,
// se ele levar um SIGKILL, o node filho fica órfão segurando a porta. Scripts compostos continuam via npm.
func nodeStart(script string) string {
	script = strings.TrimSpace(script)
	if simpleNodeRe.MatchString(script) {
		return script
	}
	return "npm start"
}

type packageJSON struct {
	Main    string            `json:"main"`
	Scripts map[string]string `json:"scripts"`
}

func readPackageJSON(dir string) (packageJSON, error) {
	var p packageJSON
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return p, errors.New("package.json não encontrado")
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("package.json inválido: %w", err)
	}
	return p, nil
}

func goTarget(dir string) (string, error) {
	if exists(dir, "main.go") {
		return ".", nil
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "cmd"))
	var cmds []string
	for _, e := range ents {
		if e.IsDir() {
			cmds = append(cmds, e.Name())
		}
	}
	if len(cmds) == 1 {
		return "./cmd/" + cmds[0], nil
	}
	return "", errors.New("não achei o pacote main: coloque um main.go na raiz, um único diretório em cmd/ ou defina o comando de build do app")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Saved é o subconjunto do plano gravado na release (<release>/.droidpaas.json) para o rollback
// poder regenerar o start.sh sem refazer o build.
type Saved struct {
	Runtime       string `json:"runtime"`
	Start         string `json:"start"`
	Venv          bool   `json:"venv"`
	HealthPath    string `json:"health_path"`
	HealthTimeout string `json:"health_timeout"`
	StaticDir     string `json:"static_dir"`
}

func (p Plan) Save(releaseDir string) error {
	b, _ := json.MarshalIndent(Saved{p.Runtime, p.Start, p.Venv, p.HealthPath, p.HealthTimeout.String(), p.StaticDir}, "", "  ")
	return os.WriteFile(filepath.Join(releaseDir, ".droidpaas.json"), b, 0o600)
}

func LoadSaved(releaseDir string) (Plan, error) {
	b, err := os.ReadFile(filepath.Join(releaseDir, ".droidpaas.json"))
	if err != nil {
		return Plan{}, fmt.Errorf("release sem plano salvo: %w", err)
	}
	var s Saved
	if err := json.Unmarshal(b, &s); err != nil {
		return Plan{}, err
	}
	d, _ := time.ParseDuration(s.HealthTimeout)
	return Plan{Runtime: s.Runtime, Start: s.Start, Venv: s.Venv, HealthPath: s.HealthPath, HealthTimeout: d, StaticDir: s.StaticDir}, nil
}
