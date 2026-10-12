package api

import (
	"bufio"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitSite cria o repo bare do app com um commit de site estático.
func (e *env) gitSite(t *testing.T, app string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git indisponível")
	}
	repo := filepath.Join(e.srv.Apps.AppDir(app), "repo.git")
	work := t.TempDir()
	run := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(work, "init", "-q", "--bare", "-b", "main", repo)
	os.WriteFile(filepath.Join(work, "index.html"), []byte("<h1>oi</h1>"), 0o644)
	w2 := t.TempDir()
	run(w2, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(w2, "index.html"), []byte("<h1>oi</h1>"), 0o644)
	run(w2, "add", ".")
	run(w2, "commit", "-qm", "site")
	run(w2, "push", "-q", repo, "main")
}

func (e *env) waitDeploy(t *testing.T, id string) map[string]any {
	t.Helper()
	for i := 0; i < 400; i++ {
		_, m, _ := e.do(t, "GET", "/api/v1/deploys/"+id, e.token, nil)
		if s := m["status"]; s != "queued" && s != "running" {
			return m
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("deploy não terminou")
	return nil
}

func TestDeployAPI(t *testing.T) {
	e := newEnv(t)
	e.do(t, "POST", "/api/v1/apps", e.token, map[string]string{"name": "blog"})
	// sem repositório/commits: ref inexistente
	if code, m, _ := e.do(t, "POST", "/api/v1/apps/blog/deploys", e.token, map[string]any{}); code != 409 || m["error"].(map[string]any)["code"] != "bad_ref" {
		t.Fatal(code, m)
	}
	e.gitSite(t, "blog")
	code, m, _ := e.do(t, "POST", "/api/v1/apps/blog/deploys", e.token, map[string]any{"ref": "main"})
	if code != 202 || m["status"] != "queued" || m["app"] != "blog" || m["trigger"] != "manual" {
		t.Fatal(code, m)
	}
	id := itoa(int64(m["id"].(float64)))
	done := e.waitDeploy(t, id)
	if done["status"] != "succeeded" || done["release_id"] == nil {
		t.Fatalf("%v", done)
	}

	// log em texto
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/deploys/"+id+"/logs", nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, _ := http.DefaultClient.Do(req)
	raw := new(strings.Builder)
	bufio.NewReader(resp.Body).WriteTo(raw)
	resp.Body.Close()
	if !strings.Contains(raw.String(), "-----> fetch") || !strings.Contains(raw.String(), "succeeded") {
		t.Fatalf("log: %s", raw)
	}

	// log por SSE (deploy já terminado): linhas numeradas + evento final; Last-Event-ID retoma
	sse := func(from string) (lines []string, last string) {
		req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/deploys/"+id+"/logs?follow=1", nil)
		req.Header.Set("Authorization", "Bearer "+e.token)
		if from != "" {
			req.Header.Set("Last-Event-ID", from)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("%v %v", err, resp.Header)
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "event: ") || strings.HasPrefix(l, "id: ") {
				lines = append(lines, l)
			}
			if strings.HasPrefix(l, "data: ") {
				last = l
			}
		}
		return
	}
	all, last := sse("")
	if !strings.Contains(last, `"status":"succeeded"`) || all[len(all)-1] != "event: end" {
		t.Fatalf("fim do SSE: %v %s", all, last)
	}
	resumed, _ := sse("3")
	if len(resumed) >= len(all) || resumed[0] != "id: 4" {
		t.Fatalf("retomada: %v", resumed[:2])
	}

	_, _, rawList := e.do(t, "GET", "/api/v1/apps/blog/deploys?limit=5", e.token, nil)
	if !strings.Contains(string(rawList), `"succeeded"`) {
		t.Fatal(string(rawList))
	}
	_, rel, _ := e.do(t, "GET", "/api/v1/apps/blog/releases?status=ready", e.token, nil)
	if len(rel["releases"].([]any)) != 1 || rel["current_release_id"] == nil {
		t.Fatal(rel)
	}
	if code, m, _ := e.do(t, "POST", "/api/v1/apps/blog/rollback", e.token, map[string]any{}); code != 409 || m["error"].(map[string]any)["code"] != "no_previous_release" {
		t.Fatal(code, m)
	}
	if code, _, _ := e.do(t, "POST", "/api/v1/deploys/"+id+"/cancel", e.token, nil); code != 409 {
		t.Fatalf("cancelar deploy terminado: %d", code)
	}
	if code, _, _ := e.do(t, "GET", "/api/v1/deploys/9999", e.token, nil); code != 404 {
		t.Fatal(code)
	}
	if code, _, _ := e.do(t, "GET", "/api/v1/deploys/"+id+"/logs", "", nil); code != 401 {
		t.Fatal(code)
	}
}
