package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Creds é o conteúdo de ~/.config/droid/config.toml (0600).
type Creds struct {
	URL   string `toml:"url"`
	Token string `toml:"token"`
}

func ConfigPath() string {
	if p := os.Getenv("DROID_CONFIG"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "droid", "config.toml")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "droid", "config.toml")
}

// LoadCreds lê o arquivo e deixa DROID_URL/DROID_TOKEN sobrescreverem.
func LoadCreds() (Creds, error) {
	var c Creds
	if b, err := os.ReadFile(ConfigPath()); err == nil {
		if err := toml.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", ConfigPath(), err)
		}
	}
	if v := os.Getenv("DROID_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("DROID_TOKEN"); v != "" {
		c.Token = v
	}
	if c.URL == "" || c.Token == "" {
		return c, ErrNotLoggedIn
	}
	return c, nil
}

var ErrNotLoggedIn = errors.New("sem credenciais: rode `droid login --url URL --token TOKEN`")

func SaveCreds(c Creds) error {
	p := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := toml.Marshal(c)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// APIError é um erro devolvido pela API no formato padrão do TSD §10.1.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return e.Message }

type API struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func New(c Creds) *API {
	return &API{Base: strings.TrimRight(c.URL, "/"), Token: c.Token, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (a *API) req(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base+"/api/v1"+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func decodeErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var e struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Code != "" {
		return &APIError{resp.StatusCode, e.Error.Code, e.Error.Message, e.Error.Details}
	}
	return &APIError{Status: resp.StatusCode, Code: "http_error", Message: fmt.Sprintf("%s: %s", resp.Status, strings.TrimSpace(string(b)))}
}

// Do envia a requisição JSON; out (opcional) recebe a resposta. 2xx sem corpo é sucesso.
func (a *API) Do(ctx context.Context, method, path string, body, out any) error {
	req, err := a.req(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("não consegui falar com %s: %w", a.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return decodeErr(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Text devolve o corpo como texto (logs sem streaming).
func (a *API) Text(ctx context.Context, path string, w io.Writer) error {
	req, err := a.req(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("não consegui falar com %s: %w", a.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return decodeErr(resp)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// Stream consome um SSE e chama fn(evento, dados) até o servidor fechar ou ctx terminar.
func (a *API) Stream(ctx context.Context, path string, fn func(event, data string) error) error {
	req, err := a.req(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	cli := &http.Client{} // sem timeout: o stream pode durar
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("não consegui falar com %s: %w", a.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return decodeErr(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var event, data string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if event != "" || data != "" {
				if err := fn(event, data); err != nil {
					return err
				}
			}
			event, data = "", ""
		case strings.HasPrefix(line, "event: "):
			event = line[len("event: "):]
		case strings.HasPrefix(line, "data: "):
			data = line[len("data: "):]
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sc.Err()
}

func Q(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			v.Set(kv[i], kv[i+1])
		}
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}
