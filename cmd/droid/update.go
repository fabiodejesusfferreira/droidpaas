package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"droidpaas/internal/client"
)

// updater atualiza o droidpaas a partir do clone do Git: busca, compila AO LADO, troca os binários, reinicia o droidd
// e, se o painel não voltar, restaura os binários e o código anteriores. Os apps e o túnel não são tocados (serviços runit próprios).
type updater struct {
	out, errw io.Writer
	src, bin  string                                                                   // clone do código; pasta dos binários instalados
	sh        func(dir string, env []string, name string, a ...string) (string, error) // saída combinada
	healthy   func() bool
	wait      time.Duration // quanto esperar o painel responder
	poll      time.Duration
}

const cloneHint = "refaça o clone (os dados em ~/droidpaas não são tocados):\n  cd ~ && mv droidpaas-src droidpaas-src.bak && git clone https://github.com/fabiodejesusfferreira/droidpaas.git droidpaas-src"

func (c *cli) update(args []string) error {
	fs := c.flags("update")
	check := fs.Bool("check", false, "só informa se há versão nova")
	ref := fs.String("ref", "", "tag, branch ou commit (padrão: o que `git pull` traria)")
	force := fs.Bool("force", false, "continua mesmo com mudanças locais no clone")
	srcFlag := fs.String("src", "", "pasta do clone (padrão: $DP_SRC ou ~/droidpaas-src)")
	if _, err := parse(fs, args); err != nil {
		return usageErr("uso: droid update [--check] [--ref TAG] [--force] [--src DIR]")
	}
	home, _ := os.UserHomeDir()
	src := firstNonEmpty(*srcFlag, os.Getenv("DP_SRC"), filepath.Join(home, "droidpaas-src"))
	bin := ""
	if h := os.Getenv("DP_HOME"); h != "" {
		bin = filepath.Join(h, "bin")
	} else if exe, err := os.Executable(); err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
		bin = filepath.Dir(exe)
	}
	url := "http://127.0.0.1:8090"
	if cr, err := client.LoadCreds(); err == nil {
		url = strings.TrimRight(cr.URL, "/")
	}
	u := &updater{out: c.out, errw: c.errw, src: src, bin: bin, sh: realSh, wait: 40 * time.Second, poll: time.Second,
		healthy: func() bool {
			r, err := (&http.Client{Timeout: 2 * time.Second}).Get(url + "/healthz")
			if err != nil {
				return false
			}
			r.Body.Close()
			return r.StatusCode == 200
		}}
	return u.run(*check, *ref, *force)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func realSh(dir string, env []string, name string, a ...string) (string, error) {
	cmd := exec.Command(name, a...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

func (u *updater) git(a ...string) (string, error) { return u.sh(u.src, nil, "git", a...) }

func (u *updater) run(check bool, ref string, force bool) error {
	if out, err := u.git("rev-parse", "--is-inside-work-tree"); err != nil || out != "true" {
		return fmt.Errorf("%s não é um clone do Git.\n%s", u.src, cloneHint)
	}
	if st, _ := u.git("status", "--porcelain"); st != "" && !force {
		return fmt.Errorf("há mudanças locais em %s; guarde-as (git stash) ou use --force", u.src)
	}
	if out, err := u.git("fetch", "--tags", "--prune", "origin"); err != nil {
		return fmt.Errorf("não consegui buscar o GitHub (sem internet?): %s", out)
	}
	target := ref
	if target == "" {
		target = "@{u}"
	}
	want, err := u.git("rev-parse", "--verify", "--quiet", target+"^{commit}")
	if err != nil {
		if ref == "" {
			return fmt.Errorf("este clone não segue uma branch (HEAD solto); use --ref TAG ou --ref main")
		}
		return fmt.Errorf("versão %q não encontrada no repositório", ref)
	}
	have, _ := u.git("rev-parse", "HEAD")
	if have == want {
		fmt.Fprintf(u.out, "já está atualizado (%s)\n", shortSHA(have))
		return nil
	}
	if log, _ := u.git("log", "--oneline", "--max-count=15", "HEAD.."+want); log != "" {
		fmt.Fprintf(u.out, "novidades:\n%s\n", log)
	}
	if check {
		fmt.Fprintf(u.out, "atualização disponível: %s → %s (rode `droid update`)\n", shortSHA(have), shortSHA(want))
		return nil
	}
	for _, n := range []string{"droidd", "droid"} {
		if _, err := os.Stat(filepath.Join(u.bin, n)); err != nil {
			return fmt.Errorf("não achei %s em %s (defina DP_HOME=~/droidpaas)", n, u.bin)
		}
	}

	branch, _ := u.git("symbolic-ref", "--quiet", "--short", "HEAD")
	revert := func() { // volta o código ao que estava (a árvore estava limpa, então o reset não perde nada)
		if branch != "" {
			u.git("checkout", "--quiet", branch)
			u.git("reset", "--hard", "--quiet", have)
		} else {
			u.git("checkout", "--quiet", "--detach", have)
		}
	}
	var out string
	if ref == "" {
		out, err = u.git("merge", "--ff-only", "--quiet", want)
	} else {
		out, err = u.git("checkout", "--quiet", "--detach", want)
	}
	if err != nil {
		return fmt.Errorf("não consegui trazer a versão nova (o clone divergiu do GitHub?): %s", out)
	}

	ver, _ := u.git("describe", "--tags", "--always")
	tmp := filepath.Join(u.bin, ".update-new")
	os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		revert()
		return err
	}
	fmt.Fprintf(u.out, "compilando %s (leva alguns minutos)…\n", ver)
	if out, err := u.sh(u.src, nil, "go", "build", "-ldflags", "-X main.version="+ver, "-o", tmp+string(filepath.Separator), "./cmd/..."); err != nil {
		revert()
		return fmt.Errorf("a compilação falhou; nada foi trocado:\n%s", out)
	}
	for _, n := range []string{"droidd", "droid"} {
		if out, err := u.sh(u.src, nil, filepath.Join(tmp, n), "version"); err != nil || out == "" {
			revert()
			return fmt.Errorf("o binário novo (%s) não roda; nada foi trocado: %s", n, out)
		}
	}

	for _, n := range []string{"droidd", "droid"} {
		cur := filepath.Join(u.bin, n)
		os.Remove(cur + ".prev")
		if err := os.Rename(cur, cur+".prev"); err != nil {
			u.restoreBins()
			revert()
			return err
		}
		if err := os.Rename(filepath.Join(tmp, n), cur); err != nil {
			u.restoreBins()
			revert()
			return err
		}
	}
	if err := u.restart(); err != nil || !u.waitHealthy() {
		fmt.Fprintln(u.errw, "o painel não voltou; restaurando a versão anterior…")
		u.restoreBins()
		revert()
		u.restart()
		if u.waitHealthy() {
			return fmt.Errorf("a versão %s não subiu; voltei à anterior (%s). Veja ~/droidpaas/logs/droidd/current", ver, shortSHA(have))
		}
		return fmt.Errorf("a versão %s não subiu e a anterior também não respondeu. Se houve migração de banco, restaure o backup mais recente em ~/droidpaas/data (droidpaas.db.bak-*) e rode `sv restart droidd`", ver)
	}
	fmt.Fprintf(u.out, "atualizado: %s → %s (%s). Recarregue o painel no navegador (Ctrl+F5).\n", shortSHA(have), shortSHA(want), ver)
	return nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (u *updater) restoreBins() {
	for _, n := range []string{"droidd", "droid"} {
		cur := filepath.Join(u.bin, n)
		if _, err := os.Stat(cur + ".prev"); err == nil {
			os.Rename(cur+".prev", cur)
		}
	}
}

func (u *updater) restart() error {
	svdir := os.Getenv("SVDIR")
	if svdir == "" {
		svdir = os.Getenv("PREFIX") + "/var/service"
	}
	out, err := u.sh(u.src, []string{"SVDIR=" + svdir}, "sv", "restart", "droidd")
	if err != nil {
		fmt.Fprintln(u.errw, "sv restart droidd:", out)
	}
	return err
}

func (u *updater) waitHealthy() bool {
	end := time.Now().Add(u.wait)
	for {
		if u.healthy() {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(u.poll)
	}
}
