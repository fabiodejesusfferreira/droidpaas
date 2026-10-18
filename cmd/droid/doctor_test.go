package main

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"droidpaas/internal/client"
	"droidpaas/internal/system"
)

type fakeInfo struct {
	name string
	mode os.FileMode
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 1 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

// healthyEnv descreve um aparelho em ordem; cada teste estraga uma coisa.
func healthyEnv() doctorEnv {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	files := map[string]os.FileMode{
		"/h/.termux/boot/droidpaas": 0o700, "/h/.ssh": 0o700, "/h/.ssh/authorized_keys": 0o600,
		"/h/droidpaas/etc/secrets/master.key": 0o600, "/h/droidpaas-src/.git": 0o755,
	}
	return doctorEnv{
		home: "/h", prefix: "/data/data/com.termux/files/usr", release: "F_DROID", dpHome: "/h/droidpaas", src: "/h/droidpaas-src",
		panelURL: "http://127.0.0.1:8090", now: func() time.Time { return now },
		stat: func(p string) (os.FileInfo, error) {
			if m, ok := files[p]; ok {
				return fakeInfo{p, m}, nil
			}
			return nil, os.ErrNotExist
		},
		run: func(time.Duration, string, ...string) (string, error) { return "{}", nil },
		probe: &system.FakeProbe{Bat: system.BatteryInfo{Percent: 80, TempC: 33, Charging: true, Health: "GOOD"},
			Mem: system.MemInfo{TotalKB: 3800000, AvailableKB: 1500000}, Dsk: system.DiskInfo{TotalBytes: 100 << 30, FreeBytes: 40 << 30}},
		healthz: func() (string, error) { return version, nil },
		system: func() (map[string]any, error) {
			return map[string]any{"tunnel": "connected", "services": map[string]any{"droidd": "run", "sshd": "run", "dp-caddy": "run", "dp-cloudflared": "run"}}, nil
		},
		lookup:   func(string) error { return nil },
		httpDate: func() (time.Time, error) { return now.Add(2 * time.Second), nil },
	}
}

func status(res []finding, name string) string {
	for _, f := range res {
		if f.Name == name {
			return f.Status
		}
	}
	return "ausente"
}

func TestDoctorHealthy(t *testing.T) {
	res := runDoctor(healthyEnv())
	for _, f := range res {
		if f.Status != "ok" && f.Status != "skip" {
			t.Errorf("%s: %s %s", f.Name, f.Status, f.Detail)
		}
	}
	for _, n := range []string{"Termux", "droidd", "sshd", "caddy", "túnel", "Termux:API", "bateria", "disco", "Termux:Boot", "master.key", "clone do código", "DNS", "relógio"} {
		if status(res, n) != "ok" {
			t.Errorf("%s = %s", n, status(res, n))
		}
	}
}

func TestDoctorProblems(t *testing.T) {
	cases := []struct {
		name, check, want string
		mod               func(*doctorEnv)
	}{
		{"Play Store", "Termux", "fail", func(e *doctorEnv) { e.release = "GOOGLE_PLAY" }},
		{"origem desconhecida", "Termux", "warn", func(e *doctorEnv) { e.release = "" }},
		{"droidd parado", "droidd", "fail", func(e *doctorEnv) { e.healthz = func() (string, error) { return "", errors.New("recusou") } }},
		{"versão diferente", "droidd", "warn", func(e *doctorEnv) { e.healthz = func() (string, error) { return "v-antiga", nil } }},
		{"sshd caído", "sshd", "fail", func(e *doctorEnv) {
			e.system = func() (map[string]any, error) {
				return map[string]any{"tunnel": "not_configured", "services": map[string]any{"sshd": "down", "dp-caddy": "run"}}, nil
			}
		}},
		{"túnel caído", "túnel", "fail", func(e *doctorEnv) {
			e.system = func() (map[string]any, error) {
				return map[string]any{"tunnel": "down", "services": map[string]any{}}, nil
			}
		}},
		{"sem login", "serviços", "skip", func(e *doctorEnv) { e.system = func() (map[string]any, error) { return nil, client.ErrNotLoggedIn } }},
		{"sem Termux:API", "Termux:API", "warn", func(e *doctorEnv) {
			e.run = func(time.Duration, string, ...string) (string, error) { return "", errors.New("não achei") }
		}},
		{"bateria quente", "bateria", "warn", func(e *doctorEnv) { e.probe.(*system.FakeProbe).Bat.TempC = 44 }},
		{"bateria degradada", "bateria", "warn", func(e *doctorEnv) { e.probe.(*system.FakeProbe).Bat.Health = "OVERHEAT" }},
		{"pouca memória", "memória", "warn", func(e *doctorEnv) { e.probe.(*system.FakeProbe).Mem.AvailableKB = 100 * 1024 }},
		{"disco cheio", "disco", "fail", func(e *doctorEnv) { e.probe.(*system.FakeProbe).Dsk.FreeBytes = 500 << 20 }},
		{"disco apertado", "disco", "warn", func(e *doctorEnv) { e.probe.(*system.FakeProbe).Dsk.FreeBytes = 2 << 30 }},
		{"sem script de boot", "Termux:Boot", "warn", func(e *doctorEnv) {
			old := e.stat
			e.stat = func(p string) (os.FileInfo, error) {
				if strings.HasSuffix(p, "boot/droidpaas") {
					return nil, os.ErrNotExist
				}
				return old(p)
			}
		}},
		{"authorized_keys gravável", "permissões SSH", "fail", func(e *doctorEnv) {
			old := e.stat
			e.stat = func(p string) (os.FileInfo, error) {
				if strings.HasSuffix(p, "authorized_keys") {
					return fakeInfo{p, 0o666}, nil
				}
				return old(p)
			}
		}},
		{"master.key aberta", "master.key", "fail", func(e *doctorEnv) {
			old := e.stat
			e.stat = func(p string) (os.FileInfo, error) {
				if strings.HasSuffix(p, "master.key") {
					return fakeInfo{p, 0o644}, nil
				}
				return old(p)
			}
		}},
		{"sem clone", "clone do código", "warn", func(e *doctorEnv) {
			old := e.stat
			e.stat = func(p string) (os.FileInfo, error) {
				if strings.HasSuffix(p, ".git") {
					return nil, os.ErrNotExist
				}
				return old(p)
			}
		}},
		{"sem DNS", "DNS", "warn", func(e *doctorEnv) { e.lookup = func(string) error { return errors.New("timeout") } }},
		{"relógio atrasado 20 min", "relógio", "fail", func(e *doctorEnv) {
			e.httpDate = func() (time.Time, error) { return e.now().Add(20 * time.Minute), nil }
		}},
		{"relógio 2 min", "relógio", "warn", func(e *doctorEnv) {
			e.httpDate = func() (time.Time, error) { return e.now().Add(-2 * time.Minute), nil }
		}},
	}
	for _, c := range cases {
		e := healthyEnv()
		c.mod(&e)
		if got := status(runDoctor(e), c.check); got != c.want {
			t.Errorf("%s: %s = %s, esperado %s", c.name, c.check, got, c.want)
		}
	}
}

func TestDoctorOfflineAndNotTermux(t *testing.T) {
	e := healthyEnv()
	e.offline = true
	e.lookup = func(string) error { t.Fatal("--offline não pode consultar DNS"); return nil }
	if got := status(runDoctor(e), "DNS"); got != "skip" {
		t.Fatalf("DNS = %s", got)
	}
	e = healthyEnv()
	e.prefix = ""
	res := runDoctor(e)
	if status(res, "Termux") != "warn" || status(res, "Termux:API") != "ausente" || status(res, "disco") != "ausente" {
		t.Fatalf("fora do Termux só deveria avisar e pular o resto: %+v", res)
	}
}

func TestDoctorRender(t *testing.T) {
	e := healthyEnv()
	e.release = "GOOGLE_PLAY"
	res := runDoctor(e)
	out := renderDoctor(res)
	if !strings.Contains(out, "✗ Termux:") || !strings.Contains(out, "→ desinstale") || !strings.Contains(out, "1 falhas") || !strings.Contains(out, "phantom process killer") {
		t.Fatal(out)
	}
	if strings.Contains(out, "→ ") && strings.Contains(renderDoctor(runDoctor(healthyEnv())), "→ ") {
		t.Fatal("quando tudo está ok não deve haver sugestões")
	}
}

func TestDoctorReportRedactsSecrets(t *testing.T) {
	e := healthyEnv()
	res := []finding{{"x", "fail", "token dp_abcdefghijk1234 em /h/segredo e senha=hunter2 e a | barra", ""}}
	out := renderReport(res, e)
	for _, leak := range []string{"dp_abcdefghijk1234", "hunter2", "/h/"} {
		if strings.Contains(out, leak) {
			t.Fatalf("vazou %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "~/segredo") || !strings.Contains(out, "[removido]") || !strings.Contains(out, "não contém tokens") {
		t.Fatal(out)
	}
}

func TestDoctorCommand(t *testing.T) {
	setup(t)
	r := droid(t, "", "doctor", "--offline")
	r.has(t, "droidd: no ar", "clone do código", "Termux:", "pulados")
	if r.code != 0 && r.code != 1 {
		t.Fatalf("exit %d", r.code)
	}
	j := droid(t, "", "doctor", "--offline", "--json")
	if !strings.Contains(j.out, `"name": "droidd"`) && !strings.Contains(j.out, `"name":"droidd"`) {
		t.Fatalf("json: %s", j.out)
	}
	droid(t, "", "doctor", "--offline", "--report").has(t, "### droid doctor", "| Verificação |")
	if droid(t, "", "doctor", "--nada").code != 2 {
		t.Fatal("flag inválida deveria ser 2")
	}
}
