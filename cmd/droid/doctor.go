package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"droidpaas/internal/client"
	"droidpaas/internal/system"
)

// finding é o resultado de uma verificação do `droid doctor`.
type finding struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail | skip
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// doctorEnv isola tudo o que o doctor lê do ambiente, para testar sem Termux.
type doctorEnv struct {
	home, prefix, release string // $HOME, $PREFIX e $TERMUX_APK_RELEASE
	dpHome, src           string // dados do servidor e clone do código
	offline               bool
	now                   func() time.Time
	stat                  func(string) (os.FileInfo, error)
	run                   func(timeout time.Duration, name string, a ...string) (string, error)
	probe                 system.DeviceProbe
	healthz               func() (version string, err error) // GET /healthz do droidd
	system                func() (map[string]any, error)     // GET /api/v1/system (precisa de login)
	lookup                func(host string) error            // DNS
	httpDate              func() (time.Time, error)          // cabeçalho Date de uma resposta HTTPS
	panelURL              string
}

func (c *cli) doctor(args []string) error {
	fs := c.flags("doctor")
	report := fs.Bool("report", false, "gera um relatório em Markdown, sem tokens, para anexar a um relato de problema")
	offline := fs.Bool("offline", false, "não faz consultas de rede (DNS e relógio)")
	if _, err := parse(fs, args); err != nil {
		return usageErr("uso: droid doctor [--report] [--offline]")
	}
	e := c.realDoctorEnv(*offline)
	res := runDoctor(e)
	var out string
	switch {
	case c.json:
		if err := c.printJSON(res); err != nil {
			return err
		}
	case *report:
		out = renderReport(res, e)
	default:
		out = renderDoctor(res)
	}
	fmt.Fprint(c.out, out)
	for _, f := range res {
		if f.Status == "fail" {
			return errors.New("há verificações com falha")
		}
	}
	return nil
}

func (c *cli) realDoctorEnv(offline bool) doctorEnv {
	home, _ := os.UserHomeDir()
	url := firstNonEmpty(os.Getenv("DROID_URL"), "http://127.0.0.1:8090")
	if cr, err := client.LoadCreds(); err == nil {
		url = strings.TrimRight(cr.URL, "/")
	}
	return doctorEnv{
		home: home, prefix: os.Getenv("PREFIX"), release: os.Getenv("TERMUX_APK_RELEASE"),
		dpHome:  firstNonEmpty(os.Getenv("DP_HOME"), filepath.Join(home, "droidpaas")),
		src:     firstNonEmpty(os.Getenv("DP_SRC"), filepath.Join(home, "droidpaas-src")),
		offline: offline, now: time.Now, stat: os.Stat, panelURL: url,
		probe: system.TermuxProbe{},
		run: func(timeout time.Duration, name string, a ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			b, err := exec.CommandContext(ctx, name, a...).CombinedOutput()
			return strings.TrimSpace(string(b)), err
		},
		healthz: func() (string, error) {
			r, err := (&http.Client{Timeout: 3 * time.Second}).Get(url + "/healthz")
			if err != nil {
				return "", err
			}
			defer r.Body.Close()
			var v struct {
				Version string `json:"version"`
			}
			json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&v)
			if r.StatusCode != 200 {
				return "", fmt.Errorf("HTTP %d", r.StatusCode)
			}
			return v.Version, nil
		},
		system: func() (map[string]any, error) {
			api, err := c.api()
			if err != nil {
				return nil, err
			}
			var s map[string]any
			return s, api.Do(context.Background(), "GET", "/system", nil, &s)
		},
		lookup: func(host string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_, err := net.DefaultResolver.LookupHost(ctx, host)
			return err
		},
		httpDate: func() (time.Time, error) {
			r, err := (&http.Client{Timeout: 5 * time.Second}).Head("https://github.com")
			if err != nil {
				return time.Time{}, err
			}
			r.Body.Close()
			return http.ParseTime(r.Header.Get("Date"))
		},
	}
}

func runDoctor(e doctorEnv) []finding {
	var out []finding
	add := func(name, status, detail, fix string) { out = append(out, finding{name, status, detail, fix}) }
	termux := e.prefix != ""

	// 1. Termux e de onde veio
	switch {
	case !termux:
		add("Termux", "warn", "isto não parece o Termux (PREFIX vazio): as verificações do aparelho foram puladas", "rode o `droid doctor` no Termux do celular onde o droidpaas está instalado")
	case e.release == "GOOGLE_PLAY":
		add("Termux", "fail", "versão da Play Store: está abandonada e o `pkg` não funciona direito", "desinstale e instale o Termux pelo F-Droid ou pelo GitHub (docs/operacao.md §1)")
	case e.release == "F_DROID" || e.release == "GITHUB":
		add("Termux", "ok", "instalado pelo "+map[string]string{"F_DROID": "F-Droid", "GITHUB": "GitHub"}[e.release], "")
	default:
		add("Termux", "warn", "não consegui identificar de onde o Termux veio", "confirme que foi instalado pelo F-Droid ou pelo GitHub, nunca pela Play Store")
	}

	// 2. droidd e versão
	ver, err := e.healthz()
	switch {
	case err != nil:
		add("droidd", "fail", "não responde em "+e.panelURL+" ("+err.Error()+")", "inicie com `sv up droidd` (abra o Termux antes, para o runsvdir subir) e veja ~/droidpaas/logs/droidd/current")
	case ver != "" && ver != version:
		add("droidd", "warn", "no ar, mas a versão ("+ver+") difere da CLI ("+version+")", "reinicie com `sv restart droidd` (ou rode `droid update`)")
	default:
		add("droidd", "ok", "no ar, versão "+firstNonEmpty(ver, "?"), "")
	}

	// 3. serviços e túnel, pela API (precisa de login)
	sys, serr := e.system()
	switch {
	case serr != nil && errors.Is(serr, client.ErrNotLoggedIn):
		add("serviços", "skip", "sem login, não dá para consultar o droidd", "rode `droid login --url URL --token TOKEN`")
	case serr != nil:
		add("serviços", "skip", "o droidd não respondeu: "+serr.Error(), "")
	default:
		svc, _ := sys["services"].(map[string]any)
		names := make([]string, 0, len(svc))
		for n := range svc {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names { // o prefixo dos serviços é configurável: casa pelo sufixo
			st, _ := svc[n].(string)
			var fix string
			switch {
			case n == "sshd":
				fix = "rode `sshd` no Termux (porta 8022); sem ele o `git push` por SSH não funciona"
			case strings.HasSuffix(n, "caddy"):
				fix = "rode `sv up " + n + "` e confira ~/droidpaas/logs/caddy/current"
			default:
				continue // droidd já foi verificado; cloudflared vem pelo estado do túnel
			}
			if st == "run" {
				add(strings.TrimPrefix(n, "dp-"), "ok", "no ar", "")
			} else {
				add(strings.TrimPrefix(n, "dp-"), "fail", "estado: "+st, fix)
			}
		}
		switch t, _ := sys["tunnel"].(string); t {
		case "connected":
			add("túnel", "ok", "conectado", "")
		case "not_configured", "":
			add("túnel", "skip", "não configurado (opcional)", "")
		case "connecting":
			add("túnel", "warn", "conectando", "aguarde alguns segundos e rode de novo; se persistir, confira o token (`droid tunnel set`)")
		default:
			add("túnel", "fail", "estado: "+t, "confira a internet e o token (`droid tunnel set --token …`); log em ~/droidpaas/logs/cloudflared/current")
		}
	}

	// 4. Termux:API, bateria, memória, disco
	if termux {
		if _, err := e.run(6*time.Second, "termux-battery-status"); err != nil {
			add("Termux:API", "warn", "não respondeu (o painel mostrará a bateria como indisponível)", "instale o app Termux:API (mesma origem do Termux) e rode `pkg install termux-api`")
		} else {
			add("Termux:API", "ok", "responde", "")
			if b, err := e.probe.Battery(context.Background()); err == nil {
				switch {
				case b.Health != "" && b.Health != "GOOD":
					add("bateria", "warn", fmt.Sprintf("saúde %s (%d %%, %.1f °C)", b.Health, b.Percent, b.TempC), "bateria degradada ligada 24/7 é risco físico: considere trocá-la")
				case b.TempC >= 42:
					add("bateria", "warn", fmt.Sprintf("%.1f °C: quente demais", b.TempC), "ventile o aparelho, tire a capa e evite sol direto")
				case b.Percent < 20 && !b.Charging:
					add("bateria", "warn", fmt.Sprintf("%d %% e sem carregar", b.Percent), "ligue o aparelho na tomada")
				default:
					add("bateria", "ok", fmt.Sprintf("%d %%, %.1f °C, saúde %s", b.Percent, b.TempC, firstNonEmpty(b.Health, "?")), "")
				}
			}
		}
		if m, err := e.probe.Memory(context.Background()); err == nil {
			mb := m.AvailableKB / 1024
			if mb < 300 {
				add("memória", "warn", fmt.Sprintf("só %d MB livres de %d MB", mb, m.TotalKB/1024), "pare apps que não usa ou feche outros aplicativos do celular")
			} else {
				add("memória", "ok", fmt.Sprintf("%d MB livres de %d MB", mb, m.TotalKB/1024), "")
			}
		}
		if d, err := e.probe.Disk(context.Background(), e.home); err == nil {
			gb := float64(d.FreeBytes) / (1 << 30)
			switch {
			case gb < 1:
				add("disco", "fail", fmt.Sprintf("só %.1f GB livres", gb), "libere espaço: apague releases e logs antigos (`droid settings set keep_releases=2`) ou arquivos do celular")
			case gb < 3:
				add("disco", "warn", fmt.Sprintf("%.1f GB livres", gb), "builds de Node e Python usam bastante espaço; libere o que puder")
			default:
				add("disco", "ok", fmt.Sprintf("%.1f GB livres", gb), "")
			}
		}
	}

	// 5. arquivos e permissões
	if termux {
		boot := filepath.Join(e.home, ".termux", "boot", "droidpaas")
		if _, err := e.stat(boot); err != nil {
			add("Termux:Boot", "warn", "não há ~/.termux/boot/droidpaas: o servidor não sobe sozinho depois de reiniciar o celular", "instale o app Termux:Boot, abra-o uma vez e rode `droidd init` de novo (ele cria o script sem alterar os seus)")
		} else {
			add("Termux:Boot", "ok", "script de boot presente (o app Termux:Boot precisa estar instalado e aberto uma vez)", "")
		}
		for _, p := range []string{filepath.Join(e.home, ".ssh"), filepath.Join(e.home, ".ssh", "authorized_keys")} {
			if fi, err := e.stat(p); err == nil && fi.Mode().Perm()&0o022 != 0 {
				add("permissões SSH", "fail", fmt.Sprintf("%s é gravável por outros (%v): o sshd recusa as chaves", display(p, e.home), fi.Mode().Perm()), "rode `chmod go-w ~/.ssh ~/.ssh/authorized_keys` (e `chmod 700 ~/.ssh`)")
				break
			}
		}
		if fi, err := e.stat(filepath.Join(e.dpHome, "etc", "secrets", "master.key")); err == nil {
			if fi.Mode().Perm()&0o077 != 0 {
				add("master.key", "fail", fmt.Sprintf("permissão %v: outros usuários poderiam ler", fi.Mode().Perm()), "rode `chmod 600 ~/droidpaas/etc/secrets/master.key`")
			} else {
				add("master.key", "ok", "permissão 0600", "")
			}
		}
	}
	if _, err := e.stat(filepath.Join(e.src, ".git")); err != nil {
		add("clone do código", "warn", display(e.src, e.home)+" não é um clone do Git: o `droid update` não vai funcionar", cloneHint)
	} else {
		add("clone do código", "ok", display(e.src, e.home)+" (o `droid update` está disponível)", "")
	}

	// 6. rede
	if e.offline {
		add("DNS", "skip", "pulado (--offline)", "")
		return out
	}
	if err := e.lookup("github.com"); err != nil {
		add("DNS", "warn", "não resolveu github.com: "+err.Error(), "confira o Wi-Fi e o DNS do aparelho; sem isso `droid update`, o GitHub e o túnel não funcionam")
		add("relógio", "skip", "depende da internet", "")
		return out
	}
	add("DNS", "ok", "resolve github.com", "")
	if t, err := e.httpDate(); err != nil {
		add("relógio", "skip", "não consegui comparar com um servidor: "+err.Error(), "")
	} else {
		d := e.now().Sub(t)
		if d < 0 {
			d = -d
		}
		switch {
		case d > 5*time.Minute:
			add("relógio", "fail", fmt.Sprintf("desvio de %s em relação à internet: certificados e códigos 2FA falham", d.Round(time.Second)), "ative a data e hora automáticas do Android (Configurações → Data e hora)")
		case d > 60*time.Second:
			add("relógio", "warn", fmt.Sprintf("desvio de %s em relação à internet", d.Round(time.Second)), "ative a data e hora automáticas do Android")
		default:
			add("relógio", "ok", fmt.Sprintf("desvio de %s", d.Round(time.Second)), "")
		}
	}
	return out
}

func display(p, home string) string {
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

var symbol = map[string]string{"ok": "✓", "warn": "!", "fail": "✗", "skip": "-"}

func renderDoctor(res []finding) string {
	var b strings.Builder
	counts := map[string]int{}
	for _, f := range res {
		counts[f.Status]++
		fmt.Fprintf(&b, "%s %s: %s\n", symbol[f.Status], f.Name, f.Detail)
		if f.Fix != "" && f.Status != "ok" {
			fmt.Fprintf(&b, "    → %s\n", f.Fix)
		}
	}
	fmt.Fprintf(&b, "\n%d ok · %d avisos · %d falhas · %d pulados\n", counts["ok"], counts["warn"], counts["fail"], counts["skip"])
	b.WriteString("Não dá para verificar daqui: a isenção de bateria e o phantom process killer do Android (veja docs/operacao.md §7).\n")
	return b.String()
}

var secretRe = regexp.MustCompile(`dp_[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]{20,}|(?i)(token|senha|password|secret)=\S+`)

// renderReport gera Markdown para colar numa issue: caminhos do usuário viram ~ e tokens são removidos.
func renderReport(res []finding, e doctorEnv) string {
	clean := func(s string) string {
		s = secretRe.ReplaceAllString(s, "[removido]")
		if e.home != "" {
			s = strings.ReplaceAll(s, e.home, "~")
		}
		return s
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### droid doctor\n\n- droid: `%s`\n- sistema: `%s/%s`, `%s`\n- Termux: `%s`\n- data: %s\n\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version(), firstNonEmpty(e.release, "desconhecido"), e.now().UTC().Format(time.RFC3339))
	b.WriteString("| | Verificação | Detalhe |\n|---|---|---|\n")
	for _, f := range res {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", symbol[f.Status], f.Name, strings.ReplaceAll(clean(f.Detail), "|", "/"))
	}
	b.WriteString("\n_Este relatório não contém tokens nem senhas. Confira antes de publicar._\n")
	return b.String()
}
