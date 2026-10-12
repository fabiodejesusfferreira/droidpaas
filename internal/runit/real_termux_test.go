//go:build termux

// Teste de integração com o runit real. Rode no aparelho: go test -tags termux ./internal/runit
package runit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func startRunsvdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("runsvdir", dir)
	if err := cmd.Start(); err != nil {
		t.Skip("runsvdir indisponível:", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return dir
}

func waitStatus(t *testing.T, m *Manager, name, state string, d time.Duration) ServiceStatus {
	t.Helper()
	end := time.Now().Add(d)
	for {
		st, _ := m.Status(context.Background(), name)
		if st.State == state {
			return st
		}
		if time.Now().After(end) {
			t.Fatalf("%s não chegou a %q em %v (último: %+v)", name, state, d, st)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestRealRunitLifecycle(t *testing.T) {
	sv := startRunsvdir(t)
	m := New(sv)
	ctx := context.Background()
	prefix := Prefix()
	logDir := filepath.Join(t.TempDir(), "log")
	spec := ServiceSpec{Name: "t-app", Run: "#!" + prefix + "/bin/sh\nexec sleep 1000\n", LogRun: LogRun(prefix, logDir), LogDir: logDir, LogConfig: LogConfig(1000000, 5), Down: true}

	t0 := time.Now()
	if err := m.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	t.Logf("Ensure (criação + detecção pelo runsvdir): %v", time.Since(t0))
	if st, err := m.Status(ctx, "t-app"); err != nil || st.State != "down" {
		t.Fatalf("criado com down: %+v %v", st, err)
	}
	if err := m.Up(ctx, "t-app"); err != nil {
		t.Fatal(err)
	}
	st := waitStatus(t, m, "t-app", "run", 5*time.Second)

	// RNF-06: kill -9 e reinício em ≤ 5 s
	syscall.Kill(st.PID, syscall.SIGKILL)
	t1 := time.Now()
	for {
		n, _ := m.Status(ctx, "t-app")
		if n.State == "run" && n.PID != st.PID {
			t.Logf("reinício após kill -9: %v", time.Since(t1))
			break
		}
		if time.Since(t1) > 5*time.Second {
			t.Fatalf("não reiniciou em 5 s: %+v", n)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := m.Down(ctx, "t-app"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "t-app", "down", 5*time.Second)
	if err := m.Restart(ctx, "t-app", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "t-app", "run", 5*time.Second)

	if err := m.Remove(ctx, "t-app"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(ctx, "t-app"); err != ErrNoService {
		t.Fatalf("depois do Remove: %v", err)
	}
	time.Sleep(6 * time.Second) // o runsvdir não deve ressuscitar o serviço
	if _, err := os.Stat(filepath.Join(sv, "t-app")); err == nil {
		t.Fatal("serviço voltou depois do Remove")
	}
}

// O script run gerado + envdir real (chpst): valida a codificação de valores (NUL = quebra de linha, vazio, aspas).
func TestRealAppRunScriptAndEnvdir(t *testing.T) {
	sv := startRunsvdir(t)
	m := New(sv)
	ctx := context.Background()
	prefix := Prefix()
	app := t.TempDir()
	rel := filepath.Join(app, "releases", "1-abc")
	os.MkdirAll(rel, 0o755)
	os.MkdirAll(filepath.Join(app, "env"), 0o700)
	os.Symlink(rel, filepath.Join(app, "current"))
	out := filepath.Join(app, "out.txt")
	os.WriteFile(filepath.Join(app, "start.sh"), []byte("#!"+prefix+"/bin/sh\nprintf '[%s][%s][%s][%s]' \"$MULTI\" \"$VAZIA\" \"$ASPAS\" \"$PWD\" > "+out+"\nexec sleep 1000\n"), 0o700)
	for k, v := range map[string]string{"MULTI": "linha1\nlinha2", "VAZIA": "", "ASPAS": `a "b" 'c' $d =e`} {
		os.WriteFile(filepath.Join(app, "env", k), EncodeEnvValue(v), 0o600)
	}
	logDir := filepath.Join(t.TempDir(), "log")
	spec := ServiceSpec{Name: "t-run", Run: AppRun(prefix, app, 10), LogRun: LogRun(prefix, logDir), LogDir: logDir, LogConfig: LogConfig(100000, 2)}
	if err := m.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "t-run", "run", 5*time.Second)
	time.Sleep(time.Second)
	got, err := os.ReadFile(out)
	if err != nil {
		lg, _ := os.ReadFile(filepath.Join(logDir, "current"))
		runS, _ := os.ReadFile(filepath.Join(sv, "t-run", "run"))
		t.Fatalf("%v\n--- log do serviço:\n%s\n--- run:\n%s", err, lg, runS)
	}
	want := "[linha1\nlinha2][][a \"b\" 'c' $d =e][" + filepath.Join(app, "current") + "]" // $PWD lógico (via symlink)
	if string(got) != want {
		t.Fatalf("ambiente dentro do app:\n got: %q\nwant: %q", got, want)
	}
	m.Remove(ctx, "t-run")
}
