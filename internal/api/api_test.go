package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"droidpaas/internal/auth"
	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/events"
	"droidpaas/internal/store"
)

type env struct {
	srv   *Server
	ts    *httptest.Server
	token string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := crypt.New(bytes.Repeat([]byte{1}, 32))
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &Server{Store: st, Events: &events.Log{DB: st.DB, Log: log}, Box: box, Cfg: cfg, Version: "test", Log: log}
	uid, err := st.CreateUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	tok, h, p, _ := auth.NewToken()
	if _, err := st.CreateToken(ctx, uid, "t", h, p, "full"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Public())
	t.Cleanup(ts.Close)
	return &env{srv, ts, tok}
}

func (e *env) do(t *testing.T, method, path, token string, body any) (int, map[string]any, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return resp.StatusCode, m, raw
}

func TestHealthzIsPublic(t *testing.T) {
	e := newEnv(t)
	if code, m, _ := e.do(t, "GET", "/healthz", "", nil); code != 200 || m["status"] != "ok" {
		t.Fatal(code, m)
	}
}

func TestAuth(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"", "dp_invalido", "abc"} {
		code, m, _ := e.do(t, "GET", "/api/v1/me", tok, nil)
		if code != 401 || m["error"].(map[string]any)["code"] != "unauthorized" {
			t.Errorf("token %q: %d %v", tok, code, m)
		}
	}
	code, m, _ := e.do(t, "GET", "/api/v1/me", e.token, nil)
	if code != 200 || m["username"] != "admin" {
		t.Fatal(code, m)
	}
}

func TestTokenLifecycleAndScopes(t *testing.T) {
	e := newEnv(t)
	code, m, _ := e.do(t, "POST", "/api/v1/tokens", e.token, map[string]string{"name": "git-ci", "scope": "git"})
	if code != 201 {
		t.Fatal(code, m)
	}
	gitTok, id := m["token"].(string), int64(m["id"].(float64))
	if !strings.HasPrefix(gitTok, "dp_") {
		t.Fatal(gitTok)
	}
	if code, _, _ := e.do(t, "GET", "/api/v1/me", gitTok, nil); code != 403 {
		t.Fatalf("token git em rota full: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/api/v1/tokens", e.token, map[string]string{"name": "x", "scope": "root"}); code != 422 {
		t.Fatalf("scope inválido: %d", code)
	}
	if code, _, raw := e.do(t, "GET", "/api/v1/tokens", e.token, nil); code != 200 || strings.Contains(string(raw), gitTok) || strings.Contains(string(raw), "token_hash") {
		t.Fatalf("listagem vaza segredo: %s", raw)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/tokens/"+itoa(id), e.token, nil); code != 204 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/tokens/"+itoa(id), e.token, nil); code != 404 {
		t.Fatalf("revogar de novo: %d", code)
	}
	if code, _, _ := e.do(t, "GET", "/api/v1/tokens", gitTok, nil); code != 401 {
		t.Fatalf("token revogado ainda autentica: %d", code)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestStrictJSON(t *testing.T) {
	e := newEnv(t)
	if code, _, _ := e.do(t, "POST", "/api/v1/tokens", e.token, map[string]any{"name": "a", "extra": 1}); code != 400 {
		t.Fatalf("campo desconhecido: %d", code)
	}
}

func TestSettings(t *testing.T) {
	e := newEnv(t)
	if code, _, _ := e.do(t, "PUT", "/api/v1/settings", e.token, map[string]string{"base_domain": "apps.exemplo.com", "keep_releases": "3"}); code != 200 {
		t.Fatal(code)
	}
	_, m, _ := e.do(t, "GET", "/api/v1/settings", e.token, nil)
	s := m["settings"].(map[string]any)
	if s["base_domain"] != "apps.exemplo.com" || s["keep_releases"] != "3" || m["tunnel_configured"] != false {
		t.Fatal(m)
	}
	for _, bad := range []map[string]string{{"base_domain": "Inválido!"}, {"keep_releases": "0"}, {"nao_existe": "x"}, {"tunnel_token": "x"}} {
		if code, _, _ := e.do(t, "PUT", "/api/v1/settings", e.token, bad); code != 422 {
			t.Errorf("%v: %d", bad, code)
		}
	}
}

func TestTunnelTokenIsEncryptedAndNeverReturned(t *testing.T) {
	e := newEnv(t)
	if code, _, _ := e.do(t, "PUT", "/api/v1/settings/tunnel", e.token, map[string]string{"token": "SEGREDO-CF"}); code != 204 {
		t.Fatal(code)
	}
	v, _, _ := e.srv.Store.Setting(context.Background(), "tunnel_token")
	if strings.Contains(v, "SEGREDO-CF") {
		t.Fatal("token do túnel gravado em claro")
	}
	if plain, err := e.srv.Box.Open([]byte(v)); err != nil || string(plain) != "SEGREDO-CF" {
		t.Fatal("não decifra")
	}
	_, m, raw := e.do(t, "GET", "/api/v1/settings", e.token, nil)
	if m["tunnel_configured"] != true || strings.Contains(string(raw), "SEGREDO") {
		t.Fatal(string(raw))
	}
	e.do(t, "PUT", "/api/v1/settings/tunnel", e.token, map[string]string{"token": ""})
	if _, m, _ := e.do(t, "GET", "/api/v1/settings", e.token, nil); m["tunnel_configured"] != false {
		t.Fatal("limpar não funcionou")
	}
}

func TestEventsPaginationAndNoSecrets(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.do(t, "POST", "/api/v1/tokens", e.token, map[string]string{"name": "t" + itoa(int64(i))})
	}
	_, _, raw := e.do(t, "GET", "/api/v1/events?limit=2", e.token, nil)
	var page1 []map[string]any
	json.Unmarshal(raw, &page1)
	if len(page1) != 2 {
		t.Fatalf("limit: %s", raw)
	}
	before := int64(page1[1]["id"].(float64))
	_, _, raw = e.do(t, "GET", "/api/v1/events?limit=200&before="+itoa(before), e.token, nil)
	var page2 []map[string]any
	json.Unmarshal(raw, &page2)
	if len(page2) != 3 || int64(page2[0]["id"].(float64)) >= before {
		t.Fatalf("before: %s", raw)
	}
	if strings.Contains(string(raw), "dp_") {
		t.Fatal("evento contém token")
	}
}

func TestUnknownRoute(t *testing.T) {
	e := newEnv(t)
	if code, m, _ := e.do(t, "GET", "/api/v1/nada", e.token, nil); code != 404 || m["error"].(map[string]any)["code"] != "not_found" {
		t.Fatal(code, m)
	}
}
