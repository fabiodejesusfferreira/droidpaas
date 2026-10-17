package web

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Logs são conteúdo NÃO confiável (TSD §12.6): podem trazer HTML, sequências ANSI e caracteres de controle.
// O html/template já escapa o HTML; aqui removemos ANSI/controle para o texto ficar legível e seguro.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

const maxLine = 4000

func Sanitize(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == utf8.RuneError:
			// descarta controles, inclusive \r e \x1b soltos
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SanitizeLine limpa uma linha e a limita a maxLine runas.
func SanitizeLine(s string) string {
	s = strings.ReplaceAll(Sanitize(s), "\n", " ")
	if utf8.RuneCountInString(s) > maxLine {
		r := []rune(s)
		s = string(r[:maxLine]) + "…"
	}
	return s
}

// SanitizeLog limpa um bloco de log linha a linha.
func SanitizeLog(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = SanitizeLine(l)
	}
	return strings.Join(lines, "\n")
}

func agoSince(now time.Time, t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < 0, d < 45*time.Second:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("há %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("há %d h", int(d.Hours()))
	default:
		return fmt.Sprintf("há %d d", int(d.Hours()/24))
	}
}

func humanKB(kb int64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1f GB", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%.0f MB", float64(kb)/(1<<10))
	default:
		return fmt.Sprintf("%d KB", kb)
	}
}

func humanBytes(b int64) string { return humanKB(b / 1024) }

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	if s == "" {
		return "—"
	}
	return s
}

func fmtDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Second:
		return "< 1 s"
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	default:
		return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// safeNext só aceita caminhos relativos do próprio painel (evita open redirect).
func safeNext(n string) string {
	if n == "" || n[0] != '/' || strings.HasPrefix(n, "//") || strings.HasPrefix(n, "/\\") || strings.ContainsAny(n, "\r\n") {
		return "/"
	}
	return n
}
