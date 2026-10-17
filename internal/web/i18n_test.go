package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	webfs "droidpaas/web"
)

func TestCatalogCoversTemplatesAndCode(t *testing.T) {
	need := map[string]string{} // chave → onde é usada
	add := func(k, where string) {
		if _, ok := ptBR[k]; !ok {
			need[k] = where
		}
	}
	// templates: {{t "chave"}} e {{tr ...}} estáticos
	tre := regexp.MustCompile(`\{\{-?\s*\(?(?:t|tr)\s+"([^"]+)"`)
	fs.WalkDir(webfs.FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(webfs.FS, p)
		for _, m := range tre.FindAllStringSubmatch(string(b), -1) {
			add(m[1], p)
		}
		// chaves dentro de atributos: (t "x") também
		for _, m := range regexp.MustCompile(`\(t\s+"([^"]+)"\)`).FindAllStringSubmatch(string(b), -1) {
			add(m[1], p)
		}
		return nil
	})
	// chaves montadas com printf: o conjunto de valores possíveis é conhecido
	for prefix, vals := range map[string][]string{
		"state":        {"running", "starting", "stopped", "crashed", "deploying", "error", "unknown"},
		"dstatus":      {"queued", "running", "succeeded", "failed", "rolled_back", "cancelled", "interrupted"},
		"trigger":      {"git_push", "manual", "rollback", "rebuild"},
		"runtime":      runtimeChoices,
		"stab":         settingsTabs,
		"tunnel.state": {"not_configured", "down", "connecting", "connected", "unknown"},
	} {
		for _, v := range vals {
			add(prefix+"."+v, "printf "+prefix)
		}
	}
	// código Go: T("…"), c.ok/c.err/c.fail com chave, Err: "…", BatteryNote
	goRe := []*regexp.Regexp{
		regexp.MustCompile(`\bT\("([a-z0-9_.]+)"\)`),
		regexp.MustCompile(`\bc\.(?:ok|err)\("([a-z0-9_.]+)"[,)]`),
		regexp.MustCompile(`\.fail\([^,]+,\s*"([a-z0-9_.]+)"`),
		regexp.MustCompile(`Err:\s*"([a-z0-9_.]+)"`),
		regexp.MustCompile(`\bd\.Err\s*=\s*"([a-z0-9_.]+)"`),
		regexp.MustCompile(`BatteryNote\s*=\s*"([a-z0-9_.]+)"`),
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "i18n.go" {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, re := range goRe {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				add(m[1], f)
			}
		}
		// ações montadas: "action." + nome + ".ok"
		for _, a := range []string{"start", "stop", "restart"} {
			add("action."+a+".ok", f)
		}
	}
	for k, where := range need {
		t.Errorf("chave %q usada em %s não existe no catálogo pt-BR", k, where)
	}
	for k, v := range ptBR {
		if strings.TrimSpace(v) == "" {
			t.Errorf("chave %q com texto vazio", k)
		}
	}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"texto normal":                             "texto normal",
		"\x1b[31mvermelho\x1b[0m":                  "vermelho",
		"\x1b[1;38;5;200mcor\x1b[K":                "cor",
		"\x1b]0;título\x07depois":                  "depois",
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\": "link",
		"a\x00b\x07c\x08d":                         "abcd",
		"tab\tok":                                  "tab\tok",
		"cr\rlf":                                   "crlf",
		"<b>html</b> fica (o template escapa)":     "<b>html</b> fica (o template escapa)",
		"\x1b[?25l":                                "",
		"acentuação ✓ 日本":                          "acentuação ✓ 日本",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, esperado %q", in, got, want)
		}
	}
	long := strings.Repeat("x", 10000)
	if got := SanitizeLine(long); len([]rune(got)) != maxLine+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("linha longa: %d runas", len([]rune(got)))
	}
	if got := SanitizeLog("a\r\nb\x1b[0m\nc"); got != "a\nb\nc" {
		t.Errorf("%q", got)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"": "/", "/": "/", "/apps/x?y=1": "/apps/x?y=1", "//evil.com": "/", "http://evil.com": "/", "/\\evil": "/", "abc": "/", "/a\r\nb": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q", in, got)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(3, 5*60*1e9)
	now := timeAt(0)
	for i := 0; i < 3; i++ {
		if b, _ := l.blocked("k", now); b {
			t.Fatal("bloqueou cedo")
		}
		l.fail("k", now)
	}
	if b, wait := l.blocked("k", now); !b || wait <= 0 {
		t.Fatal("deveria bloquear na 4ª")
	}
	if b, _ := l.blocked("outra", now); b {
		t.Fatal("chaves são independentes")
	}
	if b, _ := l.blocked("k", timeAt(6*60)); b {
		t.Fatal("a janela expira")
	}
	l.fail("z", now)
	l.reset("z")
	if len(l.fails["z"]) != 0 {
		t.Fatal("reset")
	}
}
