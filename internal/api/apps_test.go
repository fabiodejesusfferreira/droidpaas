package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"droidpaas/internal/store"
)

func TestAppsCRUD(t *testing.T) {
	e := newEnv(t)
	code, m, _ := e.do(t, "POST", "/api/v1/apps", "", map[string]string{"name": "x"})
	if code != 401 {
		t.Fatal(code)
	}
	code, m, _ = e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "minha-api", "runtime": "node"})
	if code != 201 || m["name"] != "minha-api" || m["state"] != "stopped" || m["port"].(float64) < 20000 {
		t.Fatal(code, m)
	}
	if g := m["git"].(map[string]any); !strings.HasPrefix(g["ssh"].(string), "ssh://") || !strings.HasSuffix(g["ssh"].(string), ":8022/minha-api.git") || !strings.HasSuffix(g["http"].(string), "/git/minha-api.git") {
		t.Fatalf("urls do git: %v", g)
	}
	for body, want := range map[string]int{`{"name":"Minha"}`: 422, `{"name":"admin"}`: 422, `{"name":"minha-api"}`: 409, `{"name":"x1","bogus":1}`: 400} {
		var mm map[string]string
		_ = mm
		if code, m, _ := e.doRaw(t, "POST", "/api/v1/apps", e.token, body); code != want {
			t.Errorf("%s: %d %v", body, code, m)
		}
	}
	_, _, raw := e.do(t, "GET", "/api/v1/apps", e.token, nil)
	if !strings.Contains(string(raw), `"minha-api"`) {
		t.Fatal(string(raw))
	}
	if code, _, _ := e.do(t, "GET", "/api/v1/apps/nada", e.token, nil); code != 404 {
		t.Fatal(code)
	}
	code, m, _ = e.do(t, "PATCH", "/api/v1/apps/minha-api", e.token, map[string]any{"health_path": "/health", "nice": 3})
	if code != 200 || m["health_path"] != "/health" || m["nice"].(float64) != 3 {
		t.Fatal(code, m)
	}
	if code, m, _ := e.do(t, "PATCH", "/api/v1/apps/minha-api", e.token, map[string]any{"health_path": "x"}); code != 422 || m["error"].(map[string]any)["details"].(map[string]any)["field"] != "health_path" {
		t.Fatal(code, m)
	}
	if code, m, _ := e.do(t, "POST", "/api/v1/apps/minha-api/start", e.token, nil); code != 409 || m["error"].(map[string]any)["code"] != "no_release" {
		t.Fatal(code, m)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/apps/minha-api?confirm=errado", e.token, nil); code != 422 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/apps/minha-api?confirm=minha-api", e.token, nil); code != 204 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "GET", "/api/v1/apps/minha-api", e.token, nil); code != 404 {
		t.Fatal(code)
	}
}

func TestStartStopWithRelease(t *testing.T) {
	e := newEnv(t)
	_, m, _ := e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "app"})
	id := int64(m["id"].(float64))
	r := store.Release{AppID: id, GitSHA: "a", GitRef: "main", Status: "ready", Path: "/x/1-a"}
	e.srv.Store.InsertRelease(context.Background(), &r)
	e.srv.Store.SetCurrentRelease(context.Background(), id, &r.ID)
	for _, c := range []struct{ act, state string }{{"start", "running"}, {"stop", "stopped"}, {"restart", "running"}} {
		code, m, _ := e.do(t, "POST", "/api/v1/apps/app/"+c.act, e.token, nil)
		if code != 200 || m["state"] != c.state {
			t.Fatalf("%s: %d %v", c.act, code, m)
		}
	}
}

func TestEnvAPI(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "app"})
	code, m, raw := e.do(t, "PUT", "/api/v1/apps/app/env", e.token, map[string]any{
		"set": map[string]any{"DATABASE_URL": map[string]any{"value": "postgres://seg", "secret": true}, "LOG_LEVEL": map[string]any{"value": "info"}}})
	if code != 200 || strings.Contains(string(raw), "postgres://seg") || m["restarted"] != false {
		t.Fatal(code, string(raw))
	}
	_, _, raw = e.do(t, "GET", "/api/v1/apps/app/env", e.token, nil)
	if !strings.Contains(string(raw), "••••") || !strings.Contains(string(raw), `"info"`) || strings.Contains(string(raw), "postgres") {
		t.Fatal(string(raw))
	}
	if code, m, _ := e.do(t, "GET", "/api/v1/apps/app/env/DATABASE_URL", e.token, nil); code != 200 || m["value"] != "postgres://seg" {
		t.Fatal(code, m)
	}
	_, _, raw = e.do(t, "GET", "/api/v1/events", e.token, nil)
	if !strings.Contains(string(raw), "env.revealed") || strings.Contains(string(raw), "postgres://seg") {
		t.Fatalf("revelação deve ser auditada sem o valor: %s", raw)
	}
	if code, _, _ := e.do(t, "PUT", "/api/v1/apps/app/env", e.token, map[string]any{"set": map[string]any{"PORT": map[string]any{"value": "1"}}}); code != 422 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/apps/app/env/LOG_LEVEL", e.token, nil); code != 204 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(e.srv.Apps.AppDir("app"), "env", "LOG_LEVEL")); err == nil {
		t.Fatal("arquivo do envdir deveria ter sido removido")
	}
}

func TestDomainsAndBaseDomainSetting(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "app"})
	e.do(t, "PUT", "/api/v1/settings", e.token, map[string]string{"base_domain": "apps.exemplo.com"})
	_, m, _ := e.do(t, "GET", "/api/v1/apps/app", e.token, nil)
	if d := m["domains"].([]any); len(d) != 1 || d[0] != "app.apps.exemplo.com" {
		t.Fatal(m["domains"])
	}
	if code, _, _ := e.do(t, "POST", "/api/v1/apps/app/domains", e.token, map[string]string{"hostname": "blog.exemplo.com"}); code != 201 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "POST", "/api/v1/apps/app/domains", e.token, map[string]string{"hostname": "blog.exemplo.com"}); code != 409 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/apps/app/domains/app.apps.exemplo.com", e.token, nil); code != 422 {
		t.Fatal("domínio automático não pode ser removido:", code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/apps/app/domains/blog.exemplo.com", e.token, nil); code != 204 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "DELETE", "/api/v1/apps/app/domains/blog.exemplo.com", e.token, nil); code != 404 {
		t.Fatal(code)
	}
}
