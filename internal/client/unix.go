// Package client reúne os clientes HTTP usados pela CLI `droid` e pelo hook do git.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"

	"droidpaas/internal/gitsrv"
)

// Unix devolve um http.Client que conversa com o socket Unix do droidd (sem timeout: o deploy pode demorar).
func Unix(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
}

// PostReceive entrega um push ao droidd e copia o log do deploy para w em tempo real.
// Devolve o código de saída que o droidd informou no trailer X-Droid-Exit.
func PostReceive(ctx context.Context, socket, app string, ups []gitsrv.Update, w io.Writer) (int, error) {
	body, _ := json.Marshal(map[string]any{"app": app, "updates": ups})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://droidd/internal/hook/post-receive", bytes.NewReader(body))
	if err != nil {
		return 1, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := Unix(socket).Do(req)
	if err != nil {
		return 1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 1, fmt.Errorf("droidd respondeu %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	// copia linha a linha para o git mostrar como `remote: …` assim que chegar
	rd := bufio.NewReader(resp.Body)
	for {
		line, err := rd.ReadString('\n')
		if line != "" {
			io.WriteString(w, line)
		}
		if err != nil {
			break
		}
	}
	code, perr := strconv.Atoi(resp.Trailer.Get("X-Droid-Exit"))
	if perr != nil {
		return 1, fmt.Errorf("conexão encerrada antes do fim do deploy")
	}
	return code, nil
}
