package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"droidpaas/internal/gitsrv"
)

// runGitShell é o comando forçado do authorized_keys: só git-receive-pack/git-upload-pack de um app existente (TSD §8.4.2).
func runGitShell(args []string) int {
	fs := flag.NewFlagSet("git-shell", flag.ContinueOnError)
	home := fs.String("home", "", "DP_HOME")
	key := fs.Int("key", 0, "id da chave SSH")
	if err := fs.Parse(args); err != nil || *home == "" {
		fmt.Fprintln(os.Stderr, "DroidPaaS: acesso negado")
		return 1
	}
	op, app, err := gitsrv.ParseShellCommand(os.Getenv("SSH_ORIGINAL_COMMAND"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "DroidPaaS: acesso negado")
		return 1
	}
	repo := filepath.Join(*home, "apps", app, "repo.git")
	if fi, err := os.Stat(repo); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "DroidPaaS: o app %q não existe. Crie-o antes (painel → Novo app, ou `droid apps create %s`) e repita o git push.\n", app, app)
		return 1
	}
	// o PATH do sshd é mínimo; os binários do Termux ficam em $PREFIX/bin
	env := os.Environ()
	if p := os.Getenv("PREFIX"); p != "" {
		env = append(env, "PATH="+p+"/bin:"+os.Getenv("PATH"))
	}
	env = append(env, fmt.Sprintf("DROID_KEY_ID=%d", *key))
	gitBin, err := exec.LookPath("git")
	if err != nil {
		fmt.Fprintln(os.Stderr, "DroidPaaS: git não encontrado")
		return 1
	}
	err = syscall.Exec(gitBin, []string{"git", op[len("git-"):], repo}, env)
	fmt.Fprintln(os.Stderr, "DroidPaaS: falha ao executar o git:", err)
	return 1
}
