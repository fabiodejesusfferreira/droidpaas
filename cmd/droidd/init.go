package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/term"

	"droidpaas/internal/auth"
	"droidpaas/internal/config"
	"droidpaas/internal/store"
)

// Ferramentas esperadas no Termux; init só avisa, nunca instala (TSD §9.1).
var wantTools = []string{"git", "sshd", "caddy", "sv", "chpst", "svlogd", "termux-battery-status"}

var dirs = []string{"bin", "etc/secrets", "data", "run", "apps", "logs/apps", "logs/deploys", "logs/droidd", "cache", "proxy/pages", "tmp"}

func runInit(args []string, stdin io.Reader, out, errw io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(errw)
	home := fs.String("home", defaultHome(), "diretório base ($DP_HOME)")
	user := fs.String("admin-user", "", "usuário admin (pergunta se vazio)")
	pwStdin := fs.Bool("password-stdin", false, "ler a senha de stdin sem prompt interativo")
	if err := fs.Parse(args); err != nil {
		return err
	}

	for _, t := range wantTools {
		if _, err := exec.LookPath(t); err != nil {
			fmt.Fprintf(errw, "aviso: %q não encontrado (instale com pkg install, se necessário)\n", t)
		}
	}

	cfg := config.Default()
	cfg.SetHome(*home)
	if err := cfg.Validate(); err != nil {
		return err
	}

	for _, d := range dirs {
		if err := os.MkdirAll(cfg.Dir(filepath.FromSlash(d)), 0o700); err != nil {
			return err
		}
	}
	if _, err := os.Stat(cfg.File()); errors.Is(err, os.ErrNotExist) {
		b, err := toml.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := os.WriteFile(cfg.File(), b, 0o600); err != nil {
			return err
		}
		fmt.Fprintln(out, "criado:", cfg.File())
	} else {
		fmt.Fprintln(out, "mantido (já existe):", cfg.File())
	}
	if err := ensureMasterKey(cfg.KeyPath()); err != nil {
		return err
	}

	ctx := context.Background()
	st, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	os.Chmod(cfg.DBPath(), 0o600)

	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		fmt.Fprintln(out, "admin já existe; nada a criar.")
	} else if err := createAdmin(ctx, st, *user, *pwStdin, stdin, out, errw); err != nil {
		return err
	}

	writeBootScript(cfg, out)
	fmt.Fprint(out, checklist)
	return nil
}

func ensureMasterKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	_, err = f.Write(b)
	return err
}

func createAdmin(ctx context.Context, st *store.Store, user string, pwStdin bool, stdin io.Reader, out, errw io.Writer) error {
	rd := bufio.NewReader(stdin)
	line := func(prompt string) (string, error) {
		fmt.Fprint(errw, prompt)
		s, err := rd.ReadString('\n')
		if err != nil && s == "" {
			return "", err
		}
		return strings.TrimRight(s, "\r\n"), nil
	}
	secret := func(prompt string) (string, error) {
		if f, ok := stdin.(*os.File); ok && !pwStdin && term.IsTerminal(int(f.Fd())) {
			fmt.Fprint(errw, prompt)
			b, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(errw)
			return string(b), err
		}
		return line(prompt)
	}

	if user == "" {
		var err error
		if user, err = line("usuário admin [admin]: "); err != nil {
			return err
		}
		if user == "" {
			user = "admin"
		}
	}
	pw, err := secret(fmt.Sprintf("senha (mín. %d caracteres): ", auth.MinPasswordLen))
	if err != nil {
		return err
	}
	pw2, err := secret("repita a senha: ")
	if err != nil {
		return err
	}
	if pw != pw2 {
		return errors.New("as senhas não conferem")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	uid, err := st.CreateUser(ctx, user, hash)
	if err != nil {
		return err
	}
	tok, th, prefix, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err := st.CreateToken(ctx, uid, "bootstrap", th, prefix); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nPrimeiro token de API (exibido UMA vez; guarde-o agora):\n\n  %s\n\n", tok)
	return nil
}

// writeBootScript cria ~/.termux/boot/droidpaas só no Termux e nunca altera scripts existentes (TSD §9.1, §2.5).
func writeBootScript(cfg config.Config, out io.Writer) {
	prefix := os.Getenv("PREFIX")
	home, _ := os.UserHomeDir()
	if prefix == "" || home == "" {
		return
	}
	dir := filepath.Join(home, ".termux", "boot")
	path := filepath.Join(dir, "droidpaas")
	if _, err := os.Stat(path); err == nil {
		return
	}
	body := fmt.Sprintf("#!%s/bin/sh\ntermux-wake-lock\n. %s/etc/profile.d/start-services.sh\n", prefix, prefix)
	if os.MkdirAll(dir, 0o700) == nil && os.WriteFile(path, []byte(body), 0o700) == nil {
		fmt.Fprintln(out, "criado:", path)
	}
}

const checklist = `
Próximos passos (checklist pós-instalação):
  - Desative o phantom process killer via adb (docs/fase-1/1A-ambiente.md) e isente o Termux da otimização de bateria.
  - Os serviços runit (droidd, dp-caddy) serão criados na sub-fase 2C; por ora, rode "droidd serve" à mão.
  - Configure o domínio base e o túnel quando houver domínio na Cloudflare.
`
