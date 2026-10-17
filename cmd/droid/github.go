package main

import (
	"context"
	"fmt"
)

// github liga um app a um repositório do GitHub: o webhook de push dispara busca e deploy sozinho.
func (c *cli) github(args []string) error {
	const use = "uso: droid github APP [show | connect URL [--branch B] | sync | rotate-secret | disconnect]"
	if len(args) == 0 {
		return usageErr(use)
	}
	app, rest := args[0], args[1:]
	sub := "show"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	fs := c.flags("github")
	branch := fs.String("branch", "", "branch que dispara o deploy (padrão: a do app)")
	pos, err := parse(fs, rest)
	if err != nil {
		return usageErr(use)
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	ctx, base := context.Background(), "/apps/"+app+"/github"
	var v struct {
		Connected  bool   `json:"connected"`
		URL        string `json:"url"`
		Branch     string `json:"branch"`
		Private    bool   `json:"private"`
		DeployKey  string `json:"deploy_key"`
		WebhookURL string `json:"webhook_url"`
		Secret     string `json:"secret"`
		LastError  string `json:"last_error"`
	}
	switch {
	case sub == "show" && len(pos) == 0:
		if err := api.Do(ctx, "GET", base, nil, &v); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(v)
		}
		if !v.Connected {
			fmt.Fprintf(c.out, "%s não está ligado ao GitHub. Use: droid github %s connect https://github.com/dono/repo\n", app, app)
			return nil
		}
		fmt.Fprintf(c.out, "repositório: %s (branch %s)\nwebhook:     %s\n", v.URL, v.Branch, v.WebhookURL)
		if v.LastError != "" {
			fmt.Fprintln(c.out, "último erro: "+v.LastError)
		}
		return nil
	case sub == "connect" && len(pos) == 1:
		if err := api.Do(ctx, "PUT", base, map[string]string{"url": pos[0], "branch": *branch}, &v); err != nil {
			return err
		}
		if c.json {
			return c.printJSON(v)
		}
		step := 1
		fmt.Fprintf(c.out, "%s ligado a %s (branch %s)\n\n", app, v.URL, v.Branch)
		if v.Private {
			fmt.Fprintf(c.out, "%d. No GitHub: repositório → Settings → Deploy keys → Add deploy key (sem \"Allow write access\"):\n   %s\n\n", step, v.DeployKey)
			step++
		}
		fmt.Fprintf(c.out, "%d. No GitHub: repositório → Settings → Webhooks → Add webhook:\n   Payload URL:  %s\n   Content type: application/json\n   Secret:       %s\n   Events:       Just the push event\n", step, v.WebhookURL, v.Secret)
		fmt.Fprintln(c.errw, "aviso: guarde o segredo agora; para vê-lo de novo: droid github "+app+" rotate-secret (gera outro).")
		fmt.Fprintln(c.errw, "aviso: se o painel é público, libere sem login (Cloudflare Access → Bypass) só o caminho /hooks/github/*.")
		return nil
	case sub == "rotate-secret" && len(pos) == 0:
		if err := api.Do(ctx, "POST", base+"/secret", nil, &v); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "novo segredo: %s\n(cole no webhook do GitHub)\n", v.Secret)
		return nil
	case sub == "sync" && len(pos) == 0:
		var d struct {
			ID int64 `json:"id"`
		}
		if err := api.Do(ctx, "POST", base+"/sync", nil, &d); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "deploy #%d na fila. Acompanhe: droid deploy-logs %d -f\n", d.ID, d.ID)
		return nil
	case sub == "disconnect" && len(pos) == 0:
		if err := api.Do(ctx, "DELETE", base, nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "%s desligado do GitHub (o código atual continua no ar)\n", app)
		return nil
	}
	return usageErr(use)
}
