// Package logs lê os logs de runtime gravados pelo svlogd: tail e follow por polling, sem inotify (TSD §8.2.4).
package logs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Line struct {
	TS   time.Time `json:"ts"`
	Text string    `json:"text"`
}

const tsLayout = "2006-01-02_15:04:05.00000"

// ParseLine separa o timestamp do svlogd -tt ("2026-10-10_01:11:53.26671 texto"); sem timestamp válido, TS fica zerado.
func ParseLine(s string) Line {
	if len(s) > len(tsLayout) && s[len(tsLayout)] == ' ' {
		if t, err := time.ParseInLocation(tsLayout, s[:len(tsLayout)], time.UTC); err == nil {
			return Line{TS: t, Text: s[len(tsLayout)+1:]}
		}
	}
	return Line{Text: s}
}

func splitLines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// rotated lista os arquivos @*.s do svlogd, do mais novo para o mais antigo (o nome é um TAI64N ordenável).
func rotated(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "@*"))
	sort.Sort(sort.Reverse(sort.StringSlice(m)))
	return m
}

// Tail devolve as últimas n linhas (atravessando a rotação, se preciso).
func Tail(dir string, n int) ([]Line, error) {
	var lines []string
	files := append([]string{filepath.Join(dir, "current")}, rotated(dir)...)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		lines = append(splitLines(b), lines...)
		if len(lines) >= n {
			break
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]Line, len(lines))
	for i, l := range lines {
		out[i] = ParseLine(l)
	}
	return out, nil
}

// Follow emite as últimas `tail` linhas e depois cada linha nova, sobrevivendo à rotação do svlogd.
// Bloqueia até ctx terminar. interval=0 usa 500 ms.
func Follow(ctx context.Context, dir string, tail int, interval time.Duration, emit func(Line)) error {
	if interval == 0 {
		interval = 500 * time.Millisecond
	}
	cur := filepath.Join(dir, "current")
	var f *os.File
	var partial []byte
	open := func() bool {
		if f != nil {
			return true
		}
		nf, err := os.Open(cur)
		if err != nil {
			return false
		}
		f = nf
		return true
	}
	defer func() {
		if f != nil {
			f.Close()
		}
	}()

	// linhas iniciais, sem perder o que chegar entre o tail e o primeiro poll
	if open() {
		fi, _ := f.Stat()
		size := fi.Size()
		initial, err := Tail(dir, tail)
		if err != nil {
			return err
		}
		for _, l := range initial {
			emit(l)
		}
		if _, err := f.Seek(size, io.SeekStart); err != nil {
			return err
		}
	}
	drain := func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				partial = append(partial, buf[:n]...)
				for {
					i := bytes.IndexByte(partial, '\n')
					if i < 0 {
						break
					}
					emit(ParseLine(string(partial[:i])))
					partial = partial[i+1:]
				}
			}
			if err != nil || n == 0 {
				return
			}
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		if !open() {
			continue
		}
		drain() // lê o resto do arquivo aberto, mesmo que já tenha sido renomeado pela rotação
		fi, err1 := f.Stat()
		pi, err2 := os.Stat(cur)
		if err1 == nil && err2 == nil && !os.SameFile(fi, pi) {
			f.Close()
			f, partial = nil, nil
			if open() {
				drain()
			}
		}
	}
}
