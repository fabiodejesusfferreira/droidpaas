package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"

	"droidpaas/internal/client"
	"droidpaas/internal/gitsrv"
)

// runHook implementa `droid hook post-receive`: lê "old new ref" do stdin, pede o deploy ao droidd pelo
// socket Unix e imprime o log em stderr (o git mostra como `remote: …`). Falhar não desfaz o push.
func runHook(args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "post-receive" {
		fmt.Fprintln(stderr, "uso: droid hook post-receive --app NOME --socket CAMINHO")
		return 2
	}
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	app := fs.String("app", "", "nome do app")
	socket := fs.String("socket", "", "socket Unix do droidd")
	if err := fs.Parse(args[1:]); err != nil || *app == "" || *socket == "" {
		return 2
	}
	ups := gitsrv.ReadPushLines(bufio.NewScanner(stdin))
	code, err := client.PostReceive(context.Background(), *socket, *app, ups, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "DroidPaaS: não consegui falar com o droidd (%v).\nO código foi recebido; rode `droid deploy %s` quando ele voltar.\n", err, *app)
		return 1
	}
	return code
}
