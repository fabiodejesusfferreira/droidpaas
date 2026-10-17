// droid: CLI do DroidPaaS (e subcomandos internos usados pelo sshd e pelo hook do git).
package main

import (
	"fmt"
	"io"
	"os"
)

// version é sobrescrita na compilação (-ldflags "-X main.version=$(git describe --tags --always)").
var version = "0.1.0-dev"

const usage = `uso: droid [--json] <comando> [args]

  login --url URL --token TOKEN    salva as credenciais
  apps [create NOME | rm NOME --yes]
  start|stop|restart APP
  deploy APP [--ref R] [--rebuild] [--no-follow]
  rollback APP [--release N]
  logs APP [-f] [-n 200]
  deploys APP | deploy-logs ID [-f]
  env APP [list | set K=V... [--secret] [--restart] | unset K... [--restart] | reveal K]
  domains APP [list | add HOST | rm HOST]
  keys [ls | add ARQUIVO.pub [--name N] | rm ID]
  tokens [ls | create NOME [--scope full|git] | rm ID]
  tunnel set --token T | --clear
  settings [set CHAVE=VALOR...]    (base_domain, panel_hostname, keep_releases, git_http_enabled)
  status
  backup [--out ARQUIVO] [--with-key]
  update [--check] [--ref TAG] [--force]   atualiza o droidpaas pelo clone (~/droidpaas-src) e volta atrás se falhar
  version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, usage)
		return 2
	}
	switch args[0] { // subcomandos internos: não passam pela CLI de usuário
	case "git-shell":
		return runGitShell(args[1:])
	case "hook":
		return runHook(args[1:], in, errw)
	}
	return newCLI(in, out, errw).dispatch(args)
}
