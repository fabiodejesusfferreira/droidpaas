package apps

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const maxEnvValue = 16 << 10

const Masked = "••••"

type EnvValue struct {
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type EnvEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

func reservedKey(k string) bool {
	switch k {
	case "PORT", "HOST", "NODE_ENV":
		return true
	}
	return strings.HasPrefix(k, "DP_")
}

// EnvList devolve as variáveis do usuário; segredos mascarados (§8.8).
func (s *Service) EnvList(ctx context.Context, name string) ([]EnvEntry, error) {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return nil, err
	}
	vars, err := s.Store.Env(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	out := make([]EnvEntry, 0, len(vars))
	for _, v := range vars {
		e := EnvEntry{Key: v.Key, Secret: v.IsSecret, Value: Masked}
		if !v.IsSecret {
			plain, err := s.Box.Open(v.ValueEnc)
			if err != nil {
				return nil, err
			}
			e.Value = string(plain)
		}
		out = append(out, e)
	}
	return out, nil
}

// EnvReveal devolve um valor em claro; ação auditada (sem registrar o valor).
func (s *Service) EnvReveal(ctx context.Context, name, key string) (string, error) {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return "", err
	}
	vars, err := s.Store.Env(ctx, a.ID)
	if err != nil {
		return "", err
	}
	for _, v := range vars {
		if v.Key == key {
			plain, err := s.Box.Open(v.ValueEnc)
			if err != nil {
				return "", err
			}
			s.Events.Info(ctx, "env.revealed", &a.ID, "valor de variável revelado: "+key, nil)
			return string(plain), nil
		}
	}
	return "", store.ErrNotFound
}

// EnvApply grava/remove em lote e rematerializa o envdir.
func (s *Service) EnvApply(ctx context.Context, name string, set map[string]EnvValue, unset []string) error {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return err
	}
	var rows []store.EnvVar
	for k, v := range set {
		switch {
		case !envKeyRe.MatchString(k) || len(k) > 100:
			return invalid("set."+k, "nome de variável inválido")
		case reservedKey(k):
			return invalid("set."+k, "variável reservada, injetada pelo DroidPaaS")
		case len(v.Value) > maxEnvValue || strings.ContainsRune(v.Value, 0):
			return invalid("set."+k, "valor inválido (até 16 KiB, sem NUL)")
		}
		sealed, err := s.Box.Seal([]byte(v.Value))
		if err != nil {
			return err
		}
		rows = append(rows, store.EnvVar{Key: k, ValueEnc: sealed, IsSecret: v.Secret})
	}
	if err := s.Store.ApplyEnv(ctx, a.ID, rows, unset); err != nil {
		return err
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s.Events.Info(ctx, "env.updated", &a.ID, "variáveis alteradas", map[string]any{"set": keys, "unset": unset}) // nunca os valores
	return s.MaterializeEnvFor(ctx, a)
}

// MaterializeEnvFor regenera o envdir com a release e o runtime atuais.
func (s *Service) MaterializeEnvFor(ctx context.Context, a store.App) error {
	release, runtime := "", a.Runtime
	if a.CurrentReleaseID != nil {
		if r, err := s.Store.Release(ctx, *a.CurrentReleaseID); err == nil {
			release = filepath.Base(r.Path)
			if r.Runtime != "" {
				runtime = r.Runtime
			}
		}
	}
	return s.MaterializeEnv(ctx, a, release, runtime)
}

// MaterializeEnv escreve um arquivo por variável (0600) de forma atômica e remove os que sobraram (ADR-07).
func (s *Service) MaterializeEnv(ctx context.Context, a store.App, release, runtime string) error {
	vars, err := s.Store.Env(ctx, a.ID)
	if err != nil {
		return err
	}
	m := map[string]string{}
	for _, v := range vars {
		plain, err := s.Box.Open(v.ValueEnc)
		if err != nil {
			return err
		}
		m[v.Key] = string(plain)
	}
	// padrões injetados: sobrescrevem o usuário
	m["HOST"] = "127.0.0.1"
	m["DP_APP"] = a.Name
	m["DP_RELEASE"] = release
	m["DP_DATA_DIR"] = filepath.Join(s.AppDir(a.Name), "shared")
	if a.Port != nil {
		m["PORT"] = strconv.Itoa(*a.Port)
	}
	if runtime == "node" {
		m["NODE_ENV"] = "production"
	}

	dir := filepath.Join(s.AppDir(a.Name), "env")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for k, v := range m {
		tmp := filepath.Join(dir, ".tmp-"+k)
		if err := os.WriteFile(tmp, runit.EncodeEnvValue(v), 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, k)); err != nil {
			return err
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if _, keep := m[e.Name()]; !keep {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// UserEnv devolve as variáveis do usuário em claro (usadas no build e no envdir). Nunca vai para logs/eventos.
func (s *Service) UserEnv(ctx context.Context, a store.App) (map[string]string, error) {
	vars, err := s.Store.Env(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(vars))
	for _, v := range vars {
		plain, err := s.Box.Open(v.ValueEnc)
		if err != nil {
			return nil, err
		}
		m[v.Key] = string(plain)
	}
	return m, nil
}
