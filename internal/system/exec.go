package system

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// ExecRunner executa comandos em um grupo de processos próprio; cancelar o contexto envia SIGTERM
// ao grupo todo e, após KillAfter, SIGKILL (TSD §8.3.3).
type ExecRunner struct{ KillAfter time.Duration }

func (r ExecRunner) Run(ctx context.Context, dir string, env []string, out io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		grace := r.KillAfter
		if grace == 0 {
			grace = 10 * time.Second
		}
		select {
		case <-done:
		case <-time.After(grace):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		return ctx.Err()
	}
}

// GitCLI usa o binário `git` do aparelho.
type GitCLI struct{}

var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

func git(ctx context.Context, repo string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", append([]string{"--git-dir", repo}, args...)...)
}

func (GitCLI) RevParse(ctx context.Context, repo, ref string) (string, error) {
	if !refRe.MatchString(ref) || strings.Contains(ref, "..") { // nunca deixa um ref virar opção do git
		return "", fmt.Errorf("ref inválido: %q", ref)
	}
	out, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Output()
	if err != nil {
		return "", fmt.Errorf("ref %q não encontrado no repositório", ref)
	}
	return strings.TrimSpace(string(out)), nil
}

func (GitCLI) Subject(ctx context.Context, repo, sha string) (string, error) {
	out, err := git(ctx, repo, "log", "-1", "--format=%s", sha).Output()
	return strings.TrimSpace(string(out)), err
}

func (GitCLI) Archive(ctx context.Context, repo, sha, dest string) error {
	if !regexp.MustCompile(`^[0-9a-f]{7,64}$`).MatchString(sha) {
		return fmt.Errorf("sha inválido: %q", sha)
	}
	cmd := git(ctx, repo, "archive", "--format=tar", sha)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	exErr := ExtractTar(stdout, dest)
	if exErr != nil {
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if exErr != nil {
		return exErr
	}
	if waitErr != nil {
		return fmt.Errorf("git archive: %v: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ExtractTar extrai r em dest recusando caminhos que escapem de dest, inclusive via symlinks do próprio arquivo.
func ExtractTar(r io.Reader, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	dest, _ = filepath.Abs(dest)
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		target, err := safeJoin(dest, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700|os.FileMode(h.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600|os.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		default: // hardlinks, devices etc. não existem em repositórios git normais
			return fmt.Errorf("tipo de entrada não suportado em %q", h.Name)
		}
	}
}

// safeJoin junta name a dest garantindo que o resultado fica dentro de dest e que nenhum
// componente existente do caminho é um symlink (que poderia apontar para fora).
func safeJoin(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." {
		return dest, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("caminho inseguro no arquivo: %q", name)
	}
	cur := dest
	parts := strings.Split(clean, string(filepath.Separator))
	for i, p := range parts {
		cur = filepath.Join(cur, p)
		if i == len(parts)-1 {
			break
		}
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("caminho passa por symlink: %q", name)
		}
	}
	return cur, nil
}
