package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"droidpaas/internal/client"
)

type appJSON struct {
	Name         string   `json:"name"`
	Runtime      string   `json:"runtime"`
	Resolved     string   `json:"resolved_runtime"`
	Port         *int     `json:"port"`
	Branch       string   `json:"branch"`
	State        string   `json:"state"`
	DesiredState string   `json:"desired_state"`
	Domains      []string `json:"domains"`
	Git          struct {
		SSH  string `json:"ssh"`
		HTTP string `json:"http"`
	} `json:"git"`
}

func (c *cli) login(args []string) error {
	fs := c.flags("login")
	url := fs.String("url", "", "URL do painel/API")
	token := fs.String("token", "", "token de API")
	if _, err := parse(fs, args); err != nil {
		return usageErr("uso: droid login --url URL --token TOKEN")
	}
	if *url == "" || *token == "" {
		return usageErr("uso: droid login --url URL --token TOKEN")
	}
	cr := client.Creds{URL: *url, Token: *token}
	var me struct {
		Username string `json:"username"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.New(cr).Do(ctx, "GET", "/me", nil, &me); err != nil {
		return err
	}
	if err := client.SaveCreds(cr); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "autenticado como %s em %s (credenciais em %s)\n", me.Username, *url, client.ConfigPath())
	return nil
}

func (c *cli) apps(args []string) error {
	api, err := c.api()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if len(args) == 0 {
		var list []appJSON
		if err := api.Do(ctx, "GET", "/apps", nil, &list); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(list)
		}
		rows := make([][]string, len(list))
		for i, a := range list {
			port := "-"
			if a.Port != nil {
				port = strconv.Itoa(*a.Port)
			}
			rows[i] = []string{a.Name, a.State, first(a.Resolved, a.Runtime), port, dash(strings.Join(a.Domains, ","))}
		}
		c.table([]string{"APP", "ESTADO", "RUNTIME", "PORTA", "DOMÍNIOS"}, rows)
		return nil
	}
	switch args[0] {
	case "create":
		fs := c.flags("apps create")
		rt := fs.String("runtime", "auto", "runtime")
		branch := fs.String("branch", "main", "branch de deploy")
		pos, err := parse(fs, args[1:])
		if err != nil || need(pos, 1, "apps create NOME [--runtime R] [--branch B]") != nil {
			return usageErr("uso: droid apps create NOME [--runtime R] [--branch B]")
		}
		var a appJSON
		if err := api.Do(ctx, "POST", "/apps", map[string]string{"name": pos[0], "runtime": *rt, "branch": *branch}, &a); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(a)
		}
		fmt.Fprintf(c.out, "app %q criado (porta %d)\n", a.Name, *a.Port)
		if len(a.Domains) > 0 {
			fmt.Fprintf(c.out, "domínio: %s\n", strings.Join(a.Domains, ", "))
		}
		if a.Git.SSH != "" {
			fmt.Fprintf(c.out, "\ngit remote add droid %s\ngit push droid %s\n", a.Git.SSH, a.Branch)
		}
		return nil
	case "rm":
		fs := c.flags("apps rm")
		yes := fs.Bool("yes", false, "confirma a remoção")
		pos, err := parse(fs, args[1:])
		if err != nil || need(pos, 1, "apps rm NOME --yes") != nil {
			return usageErr("uso: droid apps rm NOME --yes")
		}
		if !*yes {
			return usageErr("remover apaga o app, o código e os logs; repita com --yes")
		}
		if err := api.Do(ctx, "DELETE", "/apps/"+pos[0]+client.Q("confirm", pos[0]), nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "app %q removido\n", pos[0])
		return nil
	}
	return usageErr("uso: droid apps [create NOME | rm NOME --yes]")
}

func (c *cli) lifecycle(action string) func([]string) error {
	return func(args []string) error {
		pos, err := parse(c.flags(action), args)
		if err != nil || need(pos, 1, action+" APP") != nil {
			return usageErr("uso: droid %s APP", action)
		}
		api, err := c.api()
		if err != nil {
			return err
		}
		var a appJSON
		if err := api.Do(context.Background(), "POST", "/apps/"+pos[0]+"/"+action, nil, &a); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(a)
		}
		fmt.Fprintf(c.out, "%s: %s\n", a.Name, a.State)
		return nil
	}
}

type deployJSON struct {
	ID        int64   `json:"id"`
	App       string  `json:"app"`
	Trigger   string  `json:"trigger"`
	Status    string  `json:"status"`
	Stage     *string `json:"stage"`
	Error     *string `json:"error"`
	GitSHA    string  `json:"git_sha"`
	Rebuild   bool    `json:"rebuild"`
	QueuedAt  int64   `json:"queued_at"`
	StartedAt *int64  `json:"started_at"`
	Finished  *int64  `json:"finished_at"`
}

// follow imprime o log do deploy ao vivo e devolve o status final.
func (c *cli) follow(ctx context.Context, api *client.API, id int64) (string, error) {
	status := ""
	err := api.Stream(ctx, fmt.Sprintf("/deploys/%d/logs?follow=1", id), func(event, data string) error {
		switch event {
		case "line":
			var l struct {
				Text string `json:"text"`
			}
			json.Unmarshal([]byte(data), &l)
			fmt.Fprintln(c.out, l.Text)
		case "end":
			var e struct {
				Status string `json:"status"`
			}
			json.Unmarshal([]byte(data), &e)
			status = e.Status
		}
		return nil
	})
	return status, err
}

func (c *cli) afterDeploy(d deployJSON, follow bool, api *client.API) error {
	if c.json && !follow {
		return c.printJSON(d)
	}
	if !follow {
		fmt.Fprintf(c.out, "deploy #%d na fila (acompanhe com `droid deploy-logs %d -f`)\n", d.ID, d.ID)
		return nil
	}
	ctx, cancel := c.ctx()
	defer cancel()
	status, err := c.follow(ctx, api, d.ID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(c.errw, "\ninterrompido; o deploy #%d segue no servidor\n", d.ID)
			return nil
		}
		return err
	}
	if status != "succeeded" {
		return fmt.Errorf("deploy #%d terminou com status %q", d.ID, status)
	}
	return nil
}

func (c *cli) deploy(args []string) error {
	fs := c.flags("deploy")
	ref := fs.String("ref", "", "branch, tag ou sha (padrão: branch do app)")
	rebuild := fs.Bool("rebuild", false, "ignora caches de dependências")
	noFollow := fs.Bool("no-follow", false, "não acompanha o log")
	pos, err := parse(fs, args)
	if err != nil || need(pos, 1, "deploy APP [--ref R] [--rebuild] [--no-follow]") != nil {
		return usageErr("uso: droid deploy APP [--ref R] [--rebuild] [--no-follow]")
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	var d deployJSON
	if err := api.Do(context.Background(), "POST", "/apps/"+pos[0]+"/deploys", map[string]any{"ref": *ref, "rebuild": *rebuild}, &d); err != nil {
		return err
	}
	return c.afterDeploy(d, !*noFollow && !c.json, api)
}

func (c *cli) rollback(args []string) error {
	fs := c.flags("rollback")
	rel := fs.Int64("release", 0, "id da release (padrão: a anterior)")
	pos, err := parse(fs, args)
	if err != nil || need(pos, 1, "rollback APP [--release N]") != nil {
		return usageErr("uso: droid rollback APP [--release N]")
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	var d deployJSON
	if err := api.Do(context.Background(), "POST", "/apps/"+pos[0]+"/rollback", map[string]any{"release_id": *rel}, &d); err != nil {
		return err
	}
	return c.afterDeploy(d, !c.json, api)
}

func (c *cli) logs(args []string) error {
	fs := c.flags("logs")
	f := fs.Bool("f", false, "acompanha")
	n := fs.Int("n", 200, "linhas")
	pos, err := parse(fs, args)
	if err != nil || need(pos, 1, "logs APP [-f] [-n 200]") != nil {
		return usageErr("uso: droid logs APP [-f] [-n 200]")
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	if !*f {
		return api.Text(context.Background(), "/apps/"+pos[0]+"/logs"+client.Q("tail", strconv.Itoa(*n)), c.out)
	}
	ctx, cancel := c.ctx()
	defer cancel()
	err = api.Stream(ctx, "/apps/"+pos[0]+"/logs"+client.Q("follow", "1", "tail", strconv.Itoa(*n)), func(_, data string) error {
		var l struct {
			TS   time.Time `json:"ts"`
			Text string    `json:"text"`
		}
		if json.Unmarshal([]byte(data), &l) == nil {
			if l.TS.IsZero() {
				fmt.Fprintln(c.out, l.Text)
			} else {
				fmt.Fprintf(c.out, "%s %s\n", l.TS.Local().Format("15:04:05"), l.Text)
			}
		}
		return nil
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (c *cli) deploys(args []string) error {
	pos, err := parse(c.flags("deploys"), args)
	if err != nil || need(pos, 1, "deploys APP") != nil {
		return usageErr("uso: droid deploys APP")
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	var list []deployJSON
	if err := api.Do(context.Background(), "GET", "/apps/"+pos[0]+"/deploys?limit=30", nil, &list); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(list)
	}
	rows := make([][]string, len(list))
	for i, d := range list {
		dur := "-"
		if d.StartedAt != nil && d.Finished != nil {
			dur = (time.Duration(*d.Finished-*d.StartedAt) * time.Millisecond).Round(time.Second).String()
		}
		rows[i] = []string{"#" + strconv.FormatInt(d.ID, 10), d.Status, d.Trigger, dash(short(d.GitSHA)), dur, ts(&d.QueuedAt)}
	}
	c.table([]string{"ID", "STATUS", "ORIGEM", "COMMIT", "DURAÇÃO", "QUANDO"}, rows)
	return nil
}

func (c *cli) deployLogs(args []string) error {
	fs := c.flags("deploy-logs")
	f := fs.Bool("f", false, "acompanha")
	pos, err := parse(fs, args)
	if err != nil || need(pos, 1, "deploy-logs ID [-f]") != nil {
		return usageErr("uso: droid deploy-logs ID [-f]")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(pos[0], "#"), 10, 64)
	if err != nil {
		return usageErr("ID inválido: %q", pos[0])
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	if !*f {
		return api.Text(context.Background(), fmt.Sprintf("/deploys/%d/logs", id), c.out)
	}
	ctx, cancel := c.ctx()
	defer cancel()
	if _, err := c.follow(ctx, api, id); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func (c *cli) env(args []string) error {
	if len(args) == 0 {
		return usageErr("uso: droid env APP [list | set K=V... | unset K... | reveal K]")
	}
	app, rest := args[0], args[1:]
	api, err := c.api()
	if err != nil {
		return err
	}
	ctx := context.Background()
	sub := "list"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	switch sub {
	case "list":
		var list []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Secret bool   `json:"secret"`
		}
		if err := api.Do(ctx, "GET", "/apps/"+app+"/env", nil, &list); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(list)
		}
		for _, e := range list {
			fmt.Fprintf(c.out, "%s=%s\n", e.Key, e.Value)
		}
		return nil
	case "set", "unset":
		fs := c.flags("env " + sub)
		secret := fs.Bool("secret", false, "marca como segredo (mascarado)")
		restart := fs.Bool("restart", false, "reinicia o app depois")
		pos, err := parse(fs, rest)
		if err != nil || len(pos) == 0 {
			return usageErr("uso: droid env APP %s %s [--restart]", sub, map[string]string{"set": "K=V... [--secret]", "unset": "K..."}[sub])
		}
		body := map[string]any{}
		if sub == "set" {
			set := map[string]any{}
			for _, kv := range pos {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return usageErr("use CHAVE=VALOR (recebi %q)", kv)
				}
				set[k] = map[string]any{"value": v, "secret": *secret}
			}
			body["set"] = set
		} else {
			body["unset"] = pos
		}
		var out struct {
			Restarted bool `json:"restarted"`
		}
		if err := api.Do(ctx, "PUT", "/apps/"+app+"/env"+client.Q("restart", strconv.FormatBool(*restart)), body, &out); err != nil {
			return err
		}
		msg := "variáveis salvas (reinicie o app para valerem: `droid restart " + app + "`)"
		if out.Restarted {
			msg = "variáveis salvas e app reiniciado"
		}
		fmt.Fprintln(c.out, msg)
		return nil
	case "reveal":
		if len(rest) != 1 {
			return usageErr("uso: droid env APP reveal CHAVE")
		}
		var out struct {
			Value string `json:"value"`
		}
		if err := api.Do(ctx, "GET", "/apps/"+app+"/env/"+rest[0], nil, &out); err != nil {
			return err
		}
		fmt.Fprintln(c.out, out.Value)
		return nil
	}
	return usageErr("uso: droid env APP [list | set K=V... | unset K... | reveal K]")
}

func (c *cli) domains(args []string) error {
	if len(args) == 0 {
		return usageErr("uso: droid domains APP [list | add HOST | rm HOST]")
	}
	app, rest := args[0], args[1:]
	api, err := c.api()
	if err != nil {
		return err
	}
	sub := "list"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	ctx := context.Background()
	switch {
	case sub == "list":
		var ds []struct {
			Hostname string `json:"hostname"`
			Auto     bool   `json:"auto"`
		}
		if err := api.Do(ctx, "GET", "/apps/"+app+"/domains", nil, &ds); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(ds)
		}
		for _, d := range ds {
			tag := ""
			if d.Auto {
				tag = " (automático)"
			}
			fmt.Fprintln(c.out, d.Hostname+tag)
		}
		return nil
	case sub == "add" && len(rest) == 1:
		if err := api.Do(ctx, "POST", "/apps/"+app+"/domains", map[string]string{"hostname": rest[0]}, nil); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "domínio %s adicionado. Lembre de criar o hostname no túnel da Cloudflare apontando para o Caddy.\n", rest[0])
		return nil
	case sub == "rm" && len(rest) == 1:
		if err := api.Do(ctx, "DELETE", "/apps/"+app+"/domains/"+rest[0], nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "domínio %s removido\n", rest[0])
		return nil
	}
	return usageErr("uso: droid domains APP [list | add HOST | rm HOST]")
}

func (c *cli) keys(args []string) error {
	api, err := c.api()
	if err != nil {
		return err
	}
	ctx := context.Background()
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "ls":
		var ks []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Fingerprint string `json:"fingerprint"`
		}
		if err := api.Do(ctx, "GET", "/ssh-keys", nil, &ks); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(ks)
		}
		rows := make([][]string, len(ks))
		for i, k := range ks {
			rows[i] = []string{strconv.FormatInt(k.ID, 10), k.Name, k.Fingerprint}
		}
		c.table([]string{"ID", "NOME", "FINGERPRINT"}, rows)
		return nil
	case "add":
		fs := c.flags("keys add")
		name := fs.String("name", "", "nome da chave")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("uso: droid keys add ARQUIVO.pub|- [--name N]")
		}
		var b []byte
		if pos[0] == "-" {
			b, err = io.ReadAll(c.in)
		} else {
			b, err = os.ReadFile(pos[0])
		}
		if err != nil {
			return err
		}
		var k struct {
			ID          int64  `json:"id"`
			Fingerprint string `json:"fingerprint"`
		}
		if err := api.Do(ctx, "POST", "/ssh-keys", map[string]string{"name": *name, "public_key": string(b)}, &k); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "chave #%d adicionada (%s)\n", k.ID, k.Fingerprint)
		return nil
	case "rm":
		if len(args) != 1 {
			return usageErr("uso: droid keys rm ID")
		}
		if err := api.Do(ctx, "DELETE", "/ssh-keys/"+args[0], nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "chave removida")
		return nil
	}
	return usageErr("uso: droid keys [ls | add ARQUIVO.pub | rm ID]")
}

func (c *cli) tokens(args []string) error {
	api, err := c.api()
	if err != nil {
		return err
	}
	ctx := context.Background()
	sub := "ls"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "ls":
		var ts_ []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Prefix    string `json:"prefix"`
			Scope     string `json:"scope"`
			RevokedAt *int64 `json:"revoked_at"`
			LastUsed  *int64 `json:"last_used_at"`
		}
		if err := api.Do(ctx, "GET", "/tokens", nil, &ts_); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(ts_)
		}
		rows := make([][]string, len(ts_))
		for i, t := range ts_ {
			state := "ativo"
			if t.RevokedAt != nil {
				state = "revogado"
			}
			rows[i] = []string{strconv.FormatInt(t.ID, 10), t.Name, t.Prefix + "…", t.Scope, state, ts(t.LastUsed)}
		}
		c.table([]string{"ID", "NOME", "PREFIXO", "ESCOPO", "ESTADO", "ÚLTIMO USO"}, rows)
		return nil
	case "create":
		fs := c.flags("tokens create")
		scope := fs.String("scope", "full", "full | git")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("uso: droid tokens create NOME [--scope full|git]")
		}
		var out struct {
			Token string `json:"token"`
		}
		if err := api.Do(ctx, "POST", "/tokens", map[string]string{"name": pos[0], "scope": *scope}, &out); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(out)
		}
		fmt.Fprintf(c.out, "token (exibido UMA vez; guarde agora):\n\n  %s\n", out.Token)
		return nil
	case "rm":
		if len(args) != 1 {
			return usageErr("uso: droid tokens rm ID")
		}
		if err := api.Do(ctx, "DELETE", "/tokens/"+args[0], nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "token revogado")
		return nil
	}
	return usageErr("uso: droid tokens [ls | create NOME | rm ID]")
}

func (c *cli) tunnel(args []string) error {
	if len(args) == 0 || args[0] != "set" {
		return usageErr("uso: droid tunnel set --token TOKEN | --clear")
	}
	fs := c.flags("tunnel set")
	token := fs.String("token", "", "token do túnel da Cloudflare")
	clear := fs.Bool("clear", false, "remove o token")
	if _, err := parse(fs, args[1:]); err != nil || (*token == "") == !*clear {
		return usageErr("uso: droid tunnel set --token TOKEN | --clear")
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	if err := api.Do(context.Background(), "PUT", "/settings/tunnel", map[string]string{"token": *token}, nil); err != nil {
		return err
	}
	if *clear {
		fmt.Fprintln(c.out, "token do túnel removido")
	} else {
		fmt.Fprintln(c.out, "token do túnel salvo; o dp-cloudflared sobe em instantes")
	}
	return nil
}

func (c *cli) status(args []string) error {
	api, err := c.api()
	if err != nil {
		return err
	}
	var s map[string]any
	if err := api.Do(context.Background(), "GET", "/system", nil, &s); err != nil {
		return err
	}
	if c.json {
		return c.printJSON(s)
	}
	fmt.Fprintf(c.out, "droidd %v (%v) · no ar há %s\n", s["version"], s["go"], (time.Duration(s["uptime_s"].(float64)) * time.Second).Round(time.Second))
	svc, _ := s["services"].(map[string]any)
	names := make([]string, 0, len(svc))
	for n := range svc {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(c.out, "  %-16s %v\n", n, svc[n])
	}
	if t, ok := s["tunnel"]; ok {
		fmt.Fprintf(c.out, "  %-16s %v\n", "túnel", t)
	}
	apps, _ := s["apps"].(map[string]any)
	fmt.Fprintf(c.out, "apps: %v", apps["total"])
	if by, ok := apps["by_state"].(map[string]any); ok {
		for k, v := range by {
			fmt.Fprintf(c.out, " · %v %s", v, k)
		}
	}
	fmt.Fprintln(c.out)
	return nil
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
