// Package gitsrv gerencia os repositórios bare, o hook post-receive e o authorized_keys (TSD §8.4).
package gitsrv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"

	"droidpaas/internal/config"
	"droidpaas/internal/store"
)

const (
	beginMark = "# BEGIN DROIDPAAS"
	endMark   = "# END DROIDPAAS"
)

var appRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// ParseShellCommand valida o SSH_ORIGINAL_COMMAND: só git-receive-pack/git-upload-pack de um app com nome válido,
// em qualquer das formas que o cliente git usa ('app.git', '/app.git'). Qualquer outra coisa é negada.
func ParseShellCommand(cmd string) (op, app string, err error) {
	deny := errors.New("acesso negado")
	for _, o := range []string{"git-receive-pack", "git-upload-pack"} {
		rest, ok := strings.CutPrefix(cmd, o+" '")
		if !ok {
			continue
		}
		arg, ok := strings.CutSuffix(rest, "'")
		if !ok {
			return "", "", deny
		}
		arg = strings.TrimPrefix(arg, "/")
		name, ok := strings.CutSuffix(arg, ".git")
		if !ok || !appRe.MatchString(name) {
			return "", "", deny
		}
		return o, name, nil
	}
	return "", "", deny
}

// HookScript é o post-receive instalado em cada repo.git (TSD A.4), com caminhos absolutos
// (o ambiente do sshd tem PATH mínimo).
func HookScript(droid, socket, app string) string {
	return fmt.Sprintf("#!/bin/sh\n# Gerado pelo DroidPaaS — não edite.\nexec %s hook post-receive --app %s --socket %s\n", shq(droid), shq(app), shq(socket))
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// AuthorizedLine monta a linha gerenciada do authorized_keys (TSD §8.4.2): comando forçado, sem PTY nem forwarding.
func AuthorizedLine(droid, home string, id int64, pub string) string {
	return fmt.Sprintf(`command="%s git-shell --home %s --key %d",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty %s`,
		strings.ReplaceAll(droid, `"`, ""), strings.ReplaceAll(home, `"`, ""), id, pub)
}

// MergeManagedBlock troca (ou cria/remove) o bloco gerenciado; o resto do arquivo nunca é tocado.
func MergeManagedBlock(existing string, lines []string) string {
	var before, after []string
	state := 0 // 0 antes, 1 dentro, 2 depois
	for _, l := range strings.Split(strings.TrimRight(existing, "\n"), "\n") {
		switch {
		case l == beginMark && state == 0:
			state = 1
		case l == endMark && state == 1:
			state = 2
		case state == 0:
			before = append(before, l)
		case state == 2:
			after = append(after, l)
		}
	}
	if state == 1 { // bloco aberto sem fim: descarta tudo depois do marcador por segurança
		after = nil
	}
	out := before
	if len(out) == 1 && out[0] == "" {
		out = nil
	}
	if len(lines) > 0 {
		out = append(out, beginMark)
		out = append(out, lines...)
		out = append(out, endMark)
	}
	out = append(out, after...)
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// ParsePublicKey aceita uma única chave pública sem opções e devolve a forma canônica e o fingerprint SHA256.
func ParsePublicKey(line string) (canonical, fingerprint, comment string, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "\r\n\x00") {
		return "", "", "", errors.New("informe uma única linha com a chave pública")
	}
	pub, c, opts, rest, perr := ssh.ParseAuthorizedKey([]byte(line))
	if perr != nil {
		return "", "", "", errors.New("chave pública inválida")
	}
	if len(opts) > 0 {
		return "", "", "", errors.New("chaves com opções (command=, from=…) não são aceitas")
	}
	if len(strings.TrimSpace(string(rest))) > 0 {
		return "", "", "", errors.New("informe uma única chave")
	}
	canonical = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	return canonical, ssh.FingerprintSHA256(pub), c, nil
}

type Service struct {
	Cfg   config.Config
	Store *store.Store
}

func (s *Service) Droid() string { return s.Cfg.Dir("bin", "droid") }

// Init cria o repositório bare e instala o hook (implementa apps.RepoProvisioner).
func (s *Service) Init(ctx context.Context, app string) error {
	repo := filepath.Join(s.Cfg.Dir("apps", app), "repo.git")
	if err := os.MkdirAll(filepath.Dir(repo), 0o700); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "git", "init", "-q", "--bare", "-b", "main", repo).CombinedOutput(); err != nil {
		return fmt.Errorf("git init --bare: %v: %s", err, strings.TrimSpace(string(out)))
	}
	hook := filepath.Join(repo, "hooks", "post-receive")
	return os.WriteFile(hook, []byte(HookScript(s.Droid(), s.Cfg.SocketPath(), app)), 0o700)
}

func (s *Service) Remove(context.Context, string) error { return nil } // apps.Delete apaga o diretório do app inteiro

// SyncKeys regrava o bloco gerenciado do authorized_keys a partir das chaves cadastradas.
func (s *Service) SyncKeys(ctx context.Context) error {
	if !s.Cfg.Git.SSHEnabled {
		return nil
	}
	keys, err := s.Store.SSHKeys(ctx)
	if err != nil {
		return err
	}
	var lines []string
	for _, k := range keys {
		lines = append(lines, AuthorizedLine(s.Droid(), s.Cfg.Paths.Home, k.ID, k.PublicKey))
	}
	path := s.Cfg.Git.AuthorizedKeys
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged := MergeManagedBlock(string(old), lines)
	if merged == string(old) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".droidpaas.tmp"
	if err := os.WriteFile(tmp, []byte(merged), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadPushLines lê do stdin as linhas "<old> <new> <ref>" do post-receive.
func ReadPushLines(sc *bufio.Scanner) []Update {
	var ups []Update
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 3 {
			ups = append(ups, Update{Old: f[0], New: f[1], Ref: f[2]})
		}
	}
	return ups
}

type Update struct {
	Old string `json:"old"`
	New string `json:"new"`
	Ref string `json:"ref"`
}

const ZeroSHA = "0000000000000000000000000000000000000000"
