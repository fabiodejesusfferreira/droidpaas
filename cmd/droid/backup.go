package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"droidpaas/internal/client"
)

// backup baixa uma cópia consistente do banco (e, com --with-key, a master.key num .tar.gz). Os arquivos nascem 0600.
func (c *cli) backup(args []string) error {
	fs := c.flags("backup")
	out := fs.String("out", "", "arquivo de saída")
	withKey := fs.Bool("with-key", false, "inclui a master.key (arquivo SENSÍVEL: gera .tar.gz)")
	if _, err := parse(fs, args); err != nil {
		return usageErr("uso: droid backup [--out ARQUIVO] [--with-key]")
	}
	api, err := c.api()
	if err != nil {
		return err
	}
	name := *out
	if name == "" {
		name = "droidpaas-" + time.Now().Format("20060102-150405") + map[bool]string{true: ".tar.gz", false: ".db"}[*withKey]
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := api.Text(context.Background(), "/backup"+client.Q("key", map[bool]string{true: "1"}[*withKey]), writerOnly{f}); err != nil {
		os.Remove(name)
		return err
	}
	fmt.Fprintf(c.out, "backup gravado em %s\n", name)
	if *withKey {
		fmt.Fprintln(c.errw, "aviso: o arquivo contém a master.key; quem tiver o banco e a chave lê os segredos. Guarde-o com cuidado.")
	}
	fmt.Fprintln(c.errw, "lembrete: o código e os dados dos apps (shared/) não entram neste backup.")
	return nil
}

type writerOnly struct{ io.Writer }
