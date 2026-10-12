// Package build detecta o runtime e monta o plano de build de cada release (TSD §8.3).
package build

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// RepoConfig é o droidpaas.toml opcional na raiz do repositório (§6.3).
type RepoConfig struct {
	Runtime       string `toml:"runtime"`
	Build         string `toml:"build"`
	Start         string `toml:"start"`
	HealthPath    string `toml:"health_path"`
	HealthTimeout string `toml:"health_timeout"`
	StaticDir     string `toml:"static_dir"`
	Nice          int    `toml:"nice"`
}

func (c RepoConfig) healthTimeout() time.Duration {
	d, _ := time.ParseDuration(c.HealthTimeout)
	return d
}

// LoadRepoConfig lê droidpaas.toml (estrito: campo desconhecido é erro) e a linha `web:` do Procfile.
func LoadRepoConfig(dir string) (cfg RepoConfig, procfileWeb string, err error) {
	if b, rerr := os.ReadFile(filepath.Join(dir, "droidpaas.toml")); rerr == nil {
		dec := toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields()
		if err = dec.Decode(&cfg); err != nil {
			return cfg, "", fmt.Errorf("droidpaas.toml inválido: %w", err)
		}
		if cfg.HealthTimeout != "" {
			if _, perr := time.ParseDuration(cfg.HealthTimeout); perr != nil {
				return cfg, "", fmt.Errorf("droidpaas.toml: health_timeout inválido: %w", perr)
			}
		}
	}
	if f, rerr := os.Open(filepath.Join(dir, "Procfile")); rerr == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if cmd, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "web:"); ok {
				procfileWeb = strings.TrimSpace(cmd)
				break
			}
		}
	}
	return cfg, procfileWeb, nil
}

func exists(dir, name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }

var ErrNoRuntime = errors.New("não foi possível detectar o runtime: adicione package.json (node), requirements.txt ou pyproject.toml (python), " +
	"go.mod (go), index.html (static) ou um Procfile com `web:` (custom); ou fixe o runtime nas configurações do app / no droidpaas.toml")

// Detect aplica a ordem do §8.3.1 (a primeira evidência vence, a menos que o runtime esteja fixado).
func Detect(dir, fixed string, rc RepoConfig, staticDir string, procfileWeb string) (string, error) {
	if fixed != "" && fixed != "auto" {
		return fixed, nil
	}
	switch {
	case rc.Runtime != "" && rc.Runtime != "auto":
		return rc.Runtime, nil
	case exists(dir, "package.json"):
		return "node", nil
	case exists(dir, "requirements.txt") || exists(dir, "pyproject.toml"):
		return "python", nil
	case exists(dir, "go.mod"):
		return "go", nil
	case exists(dir, "index.html") || staticDir != "":
		return "static", nil
	case procfileWeb != "":
		return "custom", nil
	}
	return "", ErrNoRuntime
}
