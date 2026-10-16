package runit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseStatus(t *testing.T) { // saídas reais observadas no PoC (1B)
	cases := []struct {
		in      string
		state   string
		pid     int
		up      time.Duration
		wantsUp bool
		err     error
	}{
		{"run: poc-app: (pid 26814) 2s; run: log: (pid 26802) 12s", "run", 26814, 2 * time.Second, true, nil},
		{"down: poc-app: 3s; run: log: (pid 26802) 3s", "down", 0, 3 * time.Second, false, nil},
		{"down: poc-app: 0s, normally up; run: log: (pid 26802) 28s", "down", 0, 0, false, nil},
		{"down: x: 5s, want up", "down", 0, 5 * time.Second, true, nil},
		{"run: x: (pid 9) 7s, want down", "run", 9, 7 * time.Second, false, nil},
		{"fail: poc-app: unable to change to service directory: file does not exist", "unknown", 0, 0, false, ErrNoService},
		{"warning: /x/var/service/dp-app-soakgo: unable to open supervise/ok: file does not exist", "unknown", 0, 0, false, ErrNotReady},
	}
	for _, c := range cases {
		st, err := ParseStatus(c.in)
		if err != c.err || st.State != c.state || st.PID != c.pid || st.Uptime != c.up || (err == nil && st.WantsUp != c.wantsUp) {
			t.Errorf("%q => %+v err=%v", c.in, st, err)
		}
	}
	if _, err := ParseStatus("lixo"); err == nil {
		t.Error("saída inesperada deveria falhar")
	}
}

func TestScripts(t *testing.T) {
	run := AppRun("/p", "/h/apps/x", 10)
	want := "#!/p/bin/sh\nexec 2>&1\nexport PATH=/p/bin:$PATH\ncd '/h/apps/x'/current || exit 1\nexec chpst -e '/h/apps/x'/env -n 10 sh '/h/apps/x'/start.sh\n"
	if run != want {
		t.Fatalf("run:\n%s", run)
	}
	if got := LogRun("/p", "/h/logs/apps/x"); got != "#!/p/bin/sh\nexec svlogd -tt '/h/logs/apps/x'\n" {
		t.Fatal(got)
	}
	if got := LogConfig(1000000, 5); got != "s1000000\nn5\n" {
		t.Fatal(got)
	}
	if got := StartScript("/p", "npm start", false); got != "#!/p/bin/sh\nexec npm start\n" {
		t.Fatal(got)
	}
	if got := StartScript("/p", "gunicorn app:app", true); !strings.Contains(got, `PATH="$PWD/.venv/bin:$PATH"`) || !strings.HasSuffix(got, "exec gunicorn app:app\n") {
		t.Fatal(got)
	}
	for _, c := range []string{"PORT=1 node x.js", "cd a && node b", "node a | tee"} {
		if got := StartScript("/p", c, false); strings.Contains(got, "exec ") {
			t.Errorf("%q não deveria usar exec: %s", c, got)
		}
	}
	// aspas simples no caminho não escapam do quoting
	if got := LogRun("/p", "/h/it's"); !strings.Contains(got, `'/h/it'\''s'`) {
		t.Fatal(got)
	}
}

// svSpy registra os comandos e simula o runsvdir criando supervise/ok.
func newTestManager(t *testing.T) (*Manager, *[]string) {
	t.Helper()
	dir := t.TempDir()
	m := &Manager{Dir: dir}
	var calls []string
	m.sv = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.ReplaceAll(strings.Join(args, " "), dir, "$SV"))
		if args[0] == "status" {
			return []byte("run: x: (pid 5) 9s; run: log: (pid 4) 9s"), nil
		}
		return nil, nil
	}
	return m, &calls
}

func simulateRunsvdir(t *testing.T, dir, name string) {
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				os.MkdirAll(filepath.Join(dir, name, "supervise"), 0o755)
				os.WriteFile(filepath.Join(dir, name, "supervise", "ok"), nil, 0o644)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
}

func TestEnsureCreatesAtomicallyAndIsIdempotent(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()
	logDir := filepath.Join(t.TempDir(), "logs")
	spec := ServiceSpec{Name: "dp-app-x", Run: "#!/bin/sh\nrun1\n", LogRun: "#!/bin/sh\nlog\n", LogDir: logDir, LogConfig: "s1\nn2\n", Down: true}
	simulateRunsvdir(t, m.Dir, "dp-app-x")
	if err := m.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(m.Dir, "dp-app-x")
	for f, exec := range map[string]bool{"run": true, "log/run": true, "down": false} { // o umask do Termux é 077
		st, err := os.Stat(filepath.Join(d, f))
		if err != nil || (st.Mode().Perm()&0o100 != 0) != exec {
			t.Errorf("%s: %v %v", f, err, st)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(logDir, "config")); string(b) != "s1\nn2\n" {
		t.Fatal("config do svlogd ausente")
	}
	if _, err := os.Stat(filepath.Join(m.Dir, ".dp-tmp-dp-app-x")); err == nil {
		t.Fatal("diretório temporário sobrou")
	}
	// segunda chamada: atualiza o run, não recria nem mexe no `down`
	spec.Run = "#!/bin/sh\nrun2\n"
	spec.Down = false
	if err := m.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "run")); string(b) != spec.Run {
		t.Fatal("run não atualizado")
	}
	if _, err := os.Stat(filepath.Join(d, "down")); err != nil {
		t.Fatal("Ensure não deve remover o `down` de um serviço existente")
	}
}

func TestEnsureTimeout(t *testing.T) {
	m, _ := newTestManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := m.Ensure(ctx, ServiceSpec{Name: "dp-app-y", Run: "x"}); err == nil {
		t.Fatal("sem runsvdir deveria falhar")
	}
}

func TestUpDownRestartRemove(t *testing.T) {
	m, calls := newTestManager(t)
	ctx := context.Background()
	if err := m.Up(ctx, "nada"); err != ErrNoService {
		t.Fatalf("serviço inexistente: %v", err)
	}
	d := filepath.Join(m.Dir, "dp-app-x")
	os.MkdirAll(filepath.Join(d, "log"), 0o755)
	os.WriteFile(filepath.Join(d, "down"), nil, 0o644)

	if err := m.Up(ctx, "dp-app-x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "down")); err == nil {
		t.Fatal("Up deve remover o down")
	}
	m.Down(ctx, "dp-app-x")
	if _, err := os.Stat(filepath.Join(d, "down")); err != nil {
		t.Fatal("Down deve criar o down")
	}
	m.Restart(ctx, "dp-app-x", 10*time.Second)
	if _, err := os.Stat(filepath.Join(d, "down")); err == nil {
		t.Fatal("Restart deve remover o down")
	}
	st, err := m.Status(ctx, "dp-app-x")
	if err != nil || st.State != "run" || st.PID != 5 {
		t.Fatalf("%+v %v", st, err)
	}
	if err := m.Remove(ctx, "dp-app-x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d); err == nil {
		t.Fatal("Remove não apagou")
	}
	want := []string{"up $SV/dp-app-x", "down $SV/dp-app-x", "-w 10 restart $SV/dp-app-x", "status $SV/dp-app-x", "-w 10 exit $SV/dp-app-x", "-w 5 exit $SV/dp-app-x/log"}
	if strings.Join(*calls, "|") != strings.Join(want, "|") {
		t.Fatalf("comandos sv:\n%v", *calls)
	}
	if m.Remove(ctx, "dp-app-x") != nil { // idempotente
		t.Fatal("Remove deve ser idempotente")
	}
}

func TestFake(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	f.Ensure(ctx, ServiceSpec{Name: "a", Down: true})
	if st, _ := f.Status(ctx, "a"); st.State != "down" {
		t.Fatal(st)
	}
	f.Up(ctx, "a")
	st, _ := f.Status(ctx, "a")
	f.Crash("a")
	st2, _ := f.Status(ctx, "a")
	if st.State != "run" || st2.PID == st.PID {
		t.Fatal(st, st2)
	}
	if _, err := f.Status(ctx, "zz"); err != ErrNoService {
		t.Fatal(err)
	}
}
