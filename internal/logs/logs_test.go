package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func TestParseLine(t *testing.T) {
	l := ParseLine("2026-10-10_01:11:53.26671 listening on 20001")
	if l.Text != "listening on 20001" || l.TS.Format(time.RFC3339) != "2026-10-10T01:11:53Z" {
		t.Fatalf("%+v", l)
	}
	if l := ParseLine("sem timestamp"); l.Text != "sem timestamp" || !l.TS.IsZero() {
		t.Fatalf("%+v", l)
	}
	if l := ParseLine(""); l.Text != "" {
		t.Fatal(l)
	}
}

func TestTailAcrossRotation(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "@400000005f000000000000001.s"), "2026-10-10_01:00:00.00000 a\n2026-10-10_01:00:01.00000 b\n")
	write(t, filepath.Join(d, "@400000005f000000000000009.s"), "2026-10-10_01:00:02.00000 c\n")
	write(t, filepath.Join(d, "current"), "2026-10-10_01:00:03.00000 d\n2026-10-10_01:00:04.00000 e\n")
	got := func(n int) string {
		ls, err := Tail(d, n)
		if err != nil {
			t.Fatal(err)
		}
		var s []string
		for _, l := range ls {
			s = append(s, l.Text)
		}
		return strings.Join(s, ",")
	}
	for n, want := range map[int]string{2: "d,e", 3: "c,d,e", 5: "a,b,c,d,e", 50: "a,b,c,d,e", 0: ""} {
		if g := got(n); g != want {
			t.Errorf("Tail(%d) = %q, esperado %q", n, g, want)
		}
	}
	if ls, err := Tail(t.TempDir(), 5); err != nil || len(ls) != 0 {
		t.Fatalf("diretório vazio: %v %v", ls, err)
	}
}

type collector struct {
	mu sync.Mutex
	ls []string
}

func (c *collector) add(l Line)  { c.mu.Lock(); c.ls = append(c.ls, l.Text); c.mu.Unlock() }
func (c *collector) get() string { c.mu.Lock(); defer c.mu.Unlock(); return strings.Join(c.ls, ",") }
func (c *collector) waitFor(t *testing.T, want string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c.get() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("esperava %q, tenho %q", want, c.get())
}

func TestFollowNewLinesPartialAndRotation(t *testing.T) {
	d := t.TempDir()
	cur := filepath.Join(d, "current")
	write(t, cur, "velha1\nvelha2\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c collector
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, d, 1, 10*time.Millisecond, c.add) }()

	c.waitFor(t, "velha2") // só as últimas `tail` linhas
	appendTo(t, cur, "nova1\nmeta")
	c.waitFor(t, "velha2,nova1") // linha incompleta espera o \n
	appendTo(t, cur, "de\n")
	c.waitFor(t, "velha2,nova1,metade")

	// rotação do svlogd: o current vira @…s e nasce um current novo; linhas escritas no antigo antes do rename não se perdem
	appendTo(t, cur, "ultima-do-antigo\n")
	if err := os.Rename(cur, filepath.Join(d, "@400000005f000000000000001.s")); err != nil {
		t.Fatal(err)
	}
	write(t, cur, "primeira-do-novo\n")
	appendTo(t, cur, "segunda-do-novo\n")
	c.waitFor(t, "velha2,nova1,metade,ultima-do-antigo,primeira-do-novo,segunda-do-novo")

	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("%v", err)
	}
}

func TestFollowWaitsForFileToAppear(t *testing.T) {
	d := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c collector
	go Follow(ctx, d, 10, 10*time.Millisecond, c.add)
	time.Sleep(50 * time.Millisecond)
	appendTo(t, filepath.Join(d, "current"), fmt.Sprintf("%s\n", "surgiu"))
	c.waitFor(t, "surgiu")
}
