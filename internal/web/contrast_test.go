package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	webfs "droidpaas/web"
)

func lum(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	ch := func(s string) float64 {
		v, _ := strconv.ParseUint(s, 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(hex[0:2]) + 0.7152*ch(hex[2:4]) + 0.0722*ch(hex[4:6])
}

func ratio(a, b string) float64 {
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// themeVars extrai as variáveis de cor de um bloco de tema do CSS (claro = :root; escuro = bloco de data-theme="dark").
func themeVars(css, selector string) map[string]string {
	i := strings.Index(css, selector)
	if i < 0 {
		return nil
	}
	j := strings.Index(css[i:], "}")
	block := css[i : i+j]
	vars := map[string]string{}
	for _, m := range regexp.MustCompile(`--([a-z-]+):\s*(#[0-9a-fA-F]{3,6})`).FindAllStringSubmatch(block, -1) {
		vars[m[1]] = m[2]
	}
	return vars
}

// Acessibilidade (TSD §12.5): contraste mínimo WCAG AA (4,5:1 para texto) nos dois temas.
func TestColorContrastAA(t *testing.T) {
	b, _ := webfs.FS.ReadFile("static/app.css")
	css := string(b)
	light := themeVars(css, ":root {")
	dark := themeVars(css, `:root[data-theme="dark"] {`)
	if len(light) < 10 || len(dark) < 10 {
		t.Fatalf("não achei as variáveis dos temas (claro=%d escuro=%d)", len(light), len(dark))
	}
	// o tema escuro "auto" (prefers-color-scheme) repete os mesmos valores do explícito
	auto := themeVars(css, `:root[data-theme="auto"] {`)
	for k, v := range dark {
		if auto[k] != v {
			t.Errorf("tema auto/escuro divergem em --%s: %s vs %s", k, auto[k], v)
		}
	}
	pairs := [][2]string{{"text", "bg"}, {"text", "surface"}, {"muted", "surface"}, {"muted", "bg"}, {"primary", "surface"}, {"primary", "bg"},
		{"primary-text", "primary"}, {"danger", "surface"}, {"ok", "surface"}, {"warn", "surface"}, {"log-text", "log-bg"},
		{"text", "code-bg"}, {"muted", "code-bg"}, {"ok", "code-bg"}, {"danger", "code-bg"}, {"warn", "code-bg"}}
	for name, th := range map[string]map[string]string{"claro": light, "escuro": dark} {
		for _, p := range pairs {
			fg, bg := th[p[0]], th[p[1]]
			if fg == "" || bg == "" {
				t.Errorf("%s: faltam --%s ou --%s", name, p[0], p[1])
				continue
			}
			if r := ratio(fg, bg); r < 4.5 {
				t.Errorf("tema %s: --%s (%s) sobre --%s (%s) = %.2f:1 (< 4,5:1)", name, p[0], fg, p[1], bg, r)
			}
		}
		// bordas de campos e botões são componentes de UI: WCAG 1.4.11, 3:1
		for _, bg := range []string{"bg", "surface"} {
			if r := ratio(th["border-strong"], th[bg]); r < 3 {
				t.Errorf("tema %s: --border-strong sobre --%s = %.2f:1 (< 3:1)", name, bg, r)
			}
		}
	}
}
