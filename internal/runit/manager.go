package runit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Manager usa `sv` e o SVDIR do termux-services (V-02/V-09 confirmadas no PoC).
type Manager struct {
	Dir string // SVDIR, ex.: $PREFIX/var/service
	// sv executa `sv <args>`; substituível nos testes.
	sv func(ctx context.Context, args ...string) ([]byte, error)
}

func New(dir string) *Manager {
	m := &Manager{Dir: dir}
	m.sv = func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "sv", args...)
		cmd.Env = append(os.Environ(), "SVDIR="+dir)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		return out.Bytes(), err
	}
	return m
}

func (m *Manager) path(name string) string { return filepath.Join(m.Dir, name) }

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return os.Chmod(path, mode)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) populate(dir string, spec ServiceSpec) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "run"), []byte(spec.Run), 0o755); err != nil {
		return err
	}
	if spec.LogRun == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "log"), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "log", "run"), []byte(spec.LogRun), 0o755); err != nil {
		return err
	}
	if spec.LogDir != "" {
		if err := os.MkdirAll(spec.LogDir, 0o700); err != nil {
			return err
		}
		return writeAtomic(filepath.Join(spec.LogDir, "config"), []byte(spec.LogConfig), 0o600)
	}
	return nil
}

// Ensure: criação atômica (§8.2.1) — monta em .dp-tmp-<nome>/, renomeia e espera o runsvdir.
func (m *Manager) Ensure(ctx context.Context, spec ServiceSpec) error {
	dir := m.path(spec.Name)
	if _, err := os.Stat(dir); err == nil {
		return m.populate(dir, spec)
	}
	tmp := m.path(".dp-tmp-" + spec.Name)
	os.RemoveAll(tmp)
	if err := m.populate(tmp, spec); err != nil {
		return err
	}
	if spec.Down {
		if err := os.WriteFile(filepath.Join(tmp, "down"), nil, 0o644); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(dir, "supervise", "ok")); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("runsvdir não detectou %s em 15 s", spec.Name)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (m *Manager) must(ctx context.Context, args ...string) error {
	out, err := m.sv(ctx, args...)
	if err != nil {
		return fmt.Errorf("sv %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) exists(name string) error {
	if _, err := os.Stat(m.path(name)); err != nil {
		return ErrNoService
	}
	return nil
}

func (m *Manager) Up(ctx context.Context, name string) error {
	if err := m.exists(name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(m.path(name), "down")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return m.must(ctx, "up", m.path(name))
}

func (m *Manager) Down(ctx context.Context, name string) error {
	if err := m.exists(name); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(m.path(name), "down"), nil, 0o644); err != nil {
		return err
	}
	return m.must(ctx, "down", m.path(name))
}

// Restart também remove o arquivo down: um serviço parado e reiniciado volta a subir no boot.
func (m *Manager) Restart(ctx context.Context, name string, wait time.Duration) error {
	if err := m.exists(name); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(m.path(name), "down")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	secs := strconv.Itoa(max(int(wait.Seconds()), 1))
	return m.must(ctx, "-w", secs, "restart", m.path(name))
}

// Remove para o serviço, encerra os runsv (app e log) e apaga o diretório.
func (m *Manager) Remove(ctx context.Context, name string) error {
	dir := m.path(name)
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	os.WriteFile(filepath.Join(dir, "down"), nil, 0o644)
	m.sv(ctx, "-w", "10", "exit", dir) // erros ignorados: pode já estar parado
	if _, err := os.Stat(filepath.Join(dir, "log")); err == nil {
		m.sv(ctx, "-w", "5", "exit", filepath.Join(dir, "log"))
	}
	return os.RemoveAll(dir)
}

var statusRe = regexp.MustCompile(`^(run|down|finish): [^:]+: (?:\(pid (\d+)\) )?(\d+)s([^;]*)`)

// ParseStatus interpreta a saída de `sv status` (formatos observados no PoC, 1B).
func ParseStatus(out string) (ServiceStatus, error) {
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	if strings.Contains(line, "unable to change to service directory") {
		return ServiceStatus{State: "unknown"}, ErrNoService
	}
	if strings.Contains(line, "unable to open supervise/ok") {
		return ServiceStatus{State: "unknown"}, ErrNotReady
	}
	m := statusRe.FindStringSubmatch(line)
	if m == nil {
		return ServiceStatus{State: "unknown"}, fmt.Errorf("saída inesperada do sv: %q", line)
	}
	st := ServiceStatus{State: m[1]}
	st.PID, _ = strconv.Atoi(m[2])
	secs, _ := strconv.Atoi(m[3])
	st.Uptime = time.Duration(secs) * time.Second
	rest := m[4]
	switch st.State {
	case "down":
		st.WantsUp = strings.Contains(rest, "want up")
	default:
		st.WantsUp = !strings.Contains(rest, "want down")
	}
	return st, nil
}

func (m *Manager) Status(ctx context.Context, name string) (ServiceStatus, error) {
	if err := m.exists(name); err != nil {
		return ServiceStatus{State: "unknown"}, err
	}
	out, _ := m.sv(ctx, "status", m.path(name)) // sv status sai com código ≠ 0 quando está down
	return ParseStatus(string(out))
}

func (m *Manager) List(_ context.Context, prefix string) ([]string, error) {
	ents, err := os.ReadDir(m.Dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
