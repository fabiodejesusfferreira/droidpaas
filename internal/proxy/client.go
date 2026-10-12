package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ProxyClient fala com a Admin API do Caddy.
type ProxyClient interface {
	// Load aplica a config completa (POST /load).
	Load(ctx context.Context, cfg []byte) error
}

type Client struct {
	Admin string // host:porta da Admin API
	HTTP  *http.Client
}

func NewClient(admin string) *Client {
	return &Client{Admin: admin, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) Load(ctx context.Context, cfg []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.Admin+"/load", bytes.NewReader(cfg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("caddy /load: %s: %s", resp.Status, bytes.TrimSpace(b))
	}
	return nil
}
