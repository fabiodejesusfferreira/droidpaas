// droidd: plano de controle do DroidPaaS.
package main

import (
	"fmt"
	"os"
)

// version é sobrescrita na compilação (-ldflags "-X main.version=$(git describe --tags --always)").
var version = "0.1.0-dev"

const usage = `uso: droidd <comando> [flags]

  init     cria diretórios, configuração, banco, admin e o primeiro token
  serve    executa o daemon (usado pelo runit)
  version  mostra a versão
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(os.Args[2:], os.Stdin, os.Stdout, os.Stderr)
	case "serve":
		err = runServe(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "droidd:", err)
		os.Exit(1)
	}
}

func defaultHome() string {
	if h := os.Getenv("DP_HOME"); h != "" {
		return h
	}
	return "~/droidpaas"
}
