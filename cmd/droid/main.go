// droid: CLI do DroidPaaS (e subcomandos internos usados pelo sshd e pelo hook do git).
package main

import (
	"fmt"
	"os"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: droid <comando> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "git-shell":
		os.Exit(runGitShell(os.Args[2:]))
	case "hook":
		os.Exit(runHook(os.Args[2:], os.Stdin, os.Stderr))
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "droid: comando desconhecido %q\n", os.Args[1])
		os.Exit(2)
	}
}
