package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseBattery(t *testing.T) { // saída real do M22 (PoC)
	b, err := ParseBattery([]byte(`{"present":true,"health":"GOOD","plugged":"UNPLUGGED","status":"DISCHARGING","temperature":32.8,"percentage":63}`))
	if err != nil || b.Percent != 63 || b.TempC != 32.8 || b.Charging || b.Health != "GOOD" {
		t.Fatalf("%+v %v", b, err)
	}
	if b, _ := ParseBattery([]byte(`{"percentage":89,"plugged":"PLUGGED_AC","temperature":37.1}`)); !b.Charging {
		t.Fatal("PLUGGED_AC é carregando")
	}
	for _, bad := range []string{"", "lixo", `{}`, `{"temperature":30}`} {
		if _, err := ParseBattery([]byte(bad)); err == nil {
			t.Errorf("%q deveria falhar", bad)
		}
	}
}

func TestBatteryCommandMissingOrSlow(t *testing.T) {
	p := TermuxProbe{BatteryCmd: "/nao/existe"}
	if _, err := p.Battery(context.Background()); err == nil {
		t.Fatal("comando ausente deve virar indisponível")
	}
	dir := t.TempDir()
	slow := filepath.Join(dir, "slow.sh")
	os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o755)
	if _, err := (TermuxProbe{BatteryCmd: slow, Timeout: 200e6}).Battery(context.Background()); err == nil {
		t.Fatal("comando lento deve estourar o timeout")
	}
	ok := filepath.Join(dir, "ok.sh")
	os.WriteFile(ok, []byte("#!/bin/sh\necho '{\"percentage\":50,\"plugged\":\"UNPLUGGED\",\"temperature\":30.5}'\n"), 0o755)
	if b, err := (TermuxProbe{BatteryCmd: ok}).Battery(context.Background()); err != nil || b.Percent != 50 {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestParseMeminfo(t *testing.T) {
	m, err := ParseMeminfo([]byte("MemTotal:        3803604 kB\nMemFree:          124808 kB\nMemAvailable:    1542668 kB\n"))
	if err != nil || m.TotalKB != 3803604 || m.AvailableKB != 1542668 {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := ParseMeminfo([]byte("x")); err == nil {
		t.Fatal("sem MemTotal deve falhar")
	}
}

func TestDisk(t *testing.T) {
	d, err := TermuxProbe{}.Disk(context.Background(), t.TempDir())
	if err != nil || d.TotalBytes == 0 || d.FreeBytes > d.TotalBytes {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := (TermuxProbe{}).Disk(context.Background(), "/nao/existe"); err == nil {
		t.Fatal("caminho inexistente deve falhar")
	}
}

// fakeProc monta um /proc com uma árvore: 100 (sh) → 101 (node "a b)") → 102; e um processo alheio 200.
func fakeProc(t *testing.T) string {
	root := t.TempDir()
	add := func(pid, ppid int, comm string, ut, st int, rss int) {
		d := filepath.Join(root, itoa(pid))
		os.MkdirAll(d, 0o755)
		// campos: pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime stime
		os.WriteFile(filepath.Join(d, "stat"), []byte(itoa(pid)+" ("+comm+") S "+itoa(ppid)+" 1 1 0 -1 0 0 0 0 0 "+itoa(ut)+" "+itoa(st)+" 0 0 20 0 1 0 1 100 100\n"), 0o644)
		os.WriteFile(filepath.Join(d, "status"), []byte("Name:\tx\nVmRSS:\t   "+itoa(rss)+" kB\n"), 0o644)
	}
	add(100, 1, "sh", 10, 5, 1000)
	add(101, 100, "node a b)", 100, 20, 30000)
	add(102, 101, "worker", 1, 1, 2000)
	add(200, 1, "outro", 999, 999, 99999)
	os.WriteFile(filepath.Join(root, "meminfo"), []byte("MemTotal: 1000 kB\nMemAvailable: 500 kB\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "self"), 0o755) // entradas não numéricas são ignoradas
	return root
}

func itoa(n int) string { return string([]byte(fmtInt(n))) }
func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func TestProcessStatsSumsTheTree(t *testing.T) {
	p := TermuxProbe{ProcRoot: fakeProc(t)}
	st, err := p.ProcessStats(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if st.Procs != 3 || st.RSSKB != 33000 || st.CPUTicks != 15+120+2 {
		t.Fatalf("%+v", st)
	}
	st, _ = p.ProcessStats(context.Background(), 101)
	if st.Procs != 2 || st.RSSKB != 32000 {
		t.Fatalf("subárvore: %+v", st)
	}
	if _, err := p.ProcessStats(context.Background(), 4242); err == nil {
		t.Fatal("pid inexistente deve falhar")
	}
	if _, err := p.ProcessStats(context.Background(), 0); err == nil {
		t.Fatal("pid 0 deve falhar")
	}
	if m, err := p.Memory(context.Background()); err != nil || m.AvailableKB != 500 {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestStatFieldsHandlesWeirdNames(t *testing.T) {
	pp, ticks, ok := statFields([]byte("5 (a) b (c)) S 77 1 1 0 -1 0 0 0 0 0 3 4 0 0\n"))
	if !ok || pp != 77 || ticks != 7 {
		t.Fatalf("%d %d %v", pp, ticks, ok)
	}
	if _, _, ok := statFields([]byte("lixo")); ok {
		t.Fatal("lixo deve falhar")
	}
}
