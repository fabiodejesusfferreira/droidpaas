package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"droidpaas/internal/client"
)

type cli struct {
	in        io.Reader
	out, errw io.Writer
	json      bool
}

func newCLI(in io.Reader, out, errw io.Writer) *cli { return &cli{in: in, out: out, errw: errw} }

// usageError vira exit code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usageErr(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

func (c *cli) dispatch(args []string) int {
	// --json vale para qualquer comando, em qualquer posição
	rest := args[:0:0]
	for _, a := range args {
		if a == "--json" {
			c.json = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		fmt.Fprint(c.errw, usage)
		return 2
	}
	cmds := map[string]func([]string) error{
		"login": c.login, "apps": c.apps, "start": c.lifecycle("start"), "stop": c.lifecycle("stop"), "restart": c.lifecycle("restart"),
		"deploy": c.deploy, "rollback": c.rollback, "logs": c.logs, "deploys": c.deploys, "deploy-logs": c.deployLogs,
		"env": c.env, "domains": c.domains, "keys": c.keys, "tokens": c.tokens, "tunnel": c.tunnel, "status": c.status,
		"backup": c.backup, "settings": c.settings,
		"version": func([]string) error { fmt.Fprintln(c.out, version); return nil },
	}
	fn, ok := cmds[rest[0]]
	if !ok {
		fmt.Fprintf(c.errw, "droid: comando desconhecido %q\n\n%s", rest[0], usage)
		return 2
	}
	err := fn(rest[1:])
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	}
	var ue usageError
	var ae *client.APIError
	switch {
	case errors.As(err, &ue):
		fmt.Fprintln(c.errw, "droid:", ue.msg)
		return 2
	case errors.Is(err, client.ErrNotLoggedIn):
		fmt.Fprintln(c.errw, "droid:", err)
		return 3
	case errors.As(err, &ae):
		fmt.Fprintf(c.errw, "droid: %s\n", ae.Message)
		if ae.Status == 401 || ae.Status == 403 {
			return 3
		}
		return 1
	}
	fmt.Fprintln(c.errw, "droid:", err)
	return 1
}

func (c *cli) api() (*client.API, error) {
	cr, err := client.LoadCreds()
	if err != nil {
		return nil, err
	}
	return client.New(cr), nil
}

// ctx termina com Ctrl+C (o deploy segue em segundo plano no servidor).
func (c *cli) ctx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// parse aceita flags antes e depois dos argumentos posicionais (`droid logs app -f`).
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (c *cli) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// need exige exatamente n argumentos posicionais (n<0: pelo menos -n).
func need(pos []string, n int, what string) error {
	if (n >= 0 && len(pos) != n) || (n < 0 && len(pos) < -n) {
		return usageErr("uso: droid %s", what)
	}
	return nil
}

func (c *cli) printJSON(v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (c *cli) table(header []string, rows [][]string) {
	tw := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
}

func ts(ms *int64) string {
	if ms == nil {
		return "-"
	}
	return time.UnixMilli(*ms).Local().Format("02/01 15:04:05")
}

func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
