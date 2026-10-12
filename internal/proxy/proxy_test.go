package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/store"
)

var update = flag.Bool("update", false, "reescreve os golden files")

func sampleInput() Input {
	return Input{
		Listen: "127.0.0.1:18080", Admin: "127.0.0.1:12019", PanelHostname: "painel.exemplo.com", PanelAddr: "127.0.0.1:8090",
		Pages: LoadPages(""),
		Apps: []AppRoute{
			{Name: "zeta", Hosts: []string{"zeta.apps.exemplo.com"}, Kind: Stopped},
			{Name: "blog", Hosts: []string{"blog.apps.exemplo.com", "blog.exemplo.com"}, Kind: Static, Root: "/h/apps/blog/current/dist"},
			{Name: "api", Hosts: []string{"www.api.com", "api.apps.exemplo.com"}, Kind: Process, Port: 20001},
			{Name: "semdominio", Kind: Process, Port: 20002},
		},
	}
}

func TestGenerateGolden(t *testing.T) {
	got, err := Generate(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "golden.json")
	if *update {
		os.WriteFile(golden, got, 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("saída diverge do golden (rode com -update se a mudança for intencional):\n%s", got)
	}
}

func TestGenerateStructure(t *testing.T) {
	b, _ := Generate(sampleInput())
	var c struct {
		Admin struct{ Listen string }
		Apps  struct {
			HTTP struct {
				Servers map[string]struct {
					Listen []string
					Routes []struct {
						Match  []struct{ Host []string }
						Handle []struct {
							Handler    string
							StatusCode string `json:"status_code"`
						}
					}
				}
			}
		}
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	s := c.Apps.HTTP.Servers["dp"]
	var order []string
	for _, r := range s.Routes {
		if len(r.Match) == 0 {
			order = append(order, "fallback:"+r.Handle[0].StatusCode)
			continue
		}
		order = append(order, r.Match[0].Host[0]+":"+r.Handle[0].Handler)
	}
	want := "painel.exemplo.com:reverse_proxy,api.apps.exemplo.com:reverse_proxy,blog.apps.exemplo.com:file_server,zeta.apps.exemplo.com:static_response,fallback:404"
	if strings.Join(order, ",") != want {
		t.Fatalf("ordem das rotas:\n got %s\nwant %s", strings.Join(order, ","), want)
	}
	if c.Admin.Listen != "127.0.0.1:12019" || s.Listen[0] != "127.0.0.1:18080" {
		t.Fatal("endereços")
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, _ := Generate(sampleInput())
	in := sampleInput()
	in.Apps[0], in.Apps[2] = in.Apps[2], in.Apps[0]
	b, _ := Generate(in)
	if string(a) != string(b) {
		t.Fatal("a ordem de entrada não pode mudar a saída (senão o hash muda e recarrega à toa)")
	}
}

func TestClientLoad(t *testing.T) {
	var gotBody, gotType string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 100)
		n, _ := r.Body.Read(b)
		gotBody, gotType = string(b[:n]), r.Header.Get("Content-Type")
		if r.URL.Path != "/load" || r.Method != "POST" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		w.Write([]byte("config inválida"))
	}))
	defer srv.Close()
	c := NewClient(strings.TrimPrefix(srv.URL, "http://"))
	if err := c.Load(context.Background(), []byte(`{"a":1}`)); err != nil || gotBody != `{"a":1}` || gotType != "application/json" {
		t.Fatalf("%v %q %q", err, gotBody, gotType)
	}
	status = 400
	if err := c.Load(context.Background(), []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "config inválida") {
		t.Fatalf("erro deve trazer o corpo: %v", err)
	}
	if err := NewClient("127.0.0.1:1").Load(context.Background(), nil); err == nil {
		t.Fatal("Caddy fora do ar deveria falhar")
	}
}

type fakeClient struct {
	loads int
	fail  bool
}

func (f *fakeClient) Load(context.Context, []byte) error {
	f.loads++
	if f.fail {
		return errors.New("caddy fora")
	}
	return nil
}

func TestApplier(t *testing.T) {
	now := time.Unix(1000, 0)
	fc := &fakeClient{}
	var fails []time.Duration
	a := &Applier{Client: fc, Now: func() time.Time { return now }, OnFail: func(_ error, d time.Duration) { fails = append(fails, d) }}
	ctx := context.Background()

	if ok, _ := a.Apply(ctx, []byte("A")); !ok || fc.loads != 1 {
		t.Fatal("primeira aplicação")
	}
	if ok, _ := a.Apply(ctx, []byte("A")); ok || fc.loads != 1 {
		t.Fatal("config igual não deve recarregar")
	}
	a.Invalidate() // Caddy reiniciou
	if ok, _ := a.Apply(ctx, []byte("A")); !ok || fc.loads != 2 {
		t.Fatal("Invalidate deve forçar a reaplicação")
	}
	fc.fail = true
	if _, err := a.Apply(ctx, []byte("B")); err == nil {
		t.Fatal("falha esperada")
	}
	// dentro do backoff não tenta de novo
	if _, err := a.Apply(ctx, []byte("B")); err != nil || fc.loads != 3 {
		t.Fatalf("durante o backoff: err=%v loads=%d", err, fc.loads)
	}
	now = now.Add(3 * time.Second)
	a.Apply(ctx, []byte("B")) // 2ª falha → 4 s
	now = now.Add(5 * time.Second)
	fc.fail = false
	if ok, _ := a.Apply(ctx, []byte("B")); !ok {
		t.Fatal("deveria aplicar depois do backoff")
	}
	if len(fails) != 2 || fails[0] != 2*time.Second || fails[1] != 4*time.Second {
		t.Fatalf("backoff: %v", fails)
	}
	// o backoff teto é 60 s
	fc.fail = true
	for i := 0; i < 20; i++ {
		now = now.Add(2 * time.Minute)
		a.Apply(ctx, []byte(string(rune('C'+i))))
	}
	if fails[len(fails)-1] != 60*time.Second {
		t.Fatalf("teto: %v", fails[len(fails)-1])
	}
}

func TestBuildInput(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.SetHome("/h")
	add := func(name, state string, rt string, port int, crashed bool) {
		p := port
		a := store.App{Name: name, Runtime: "auto", Port: &p, Branch: "main", HealthTimeoutS: 30, DesiredState: state}
		st.InsertApp(ctx, &a)
		st.AddDomain(ctx, a.ID, name+".ex.com", true, true)
		if rt != "" {
			r := store.Release{AppID: a.ID, GitSHA: "a", GitRef: "main", Status: "ready", Path: "/x", Runtime: rt}
			st.InsertRelease(ctx, &r)
			st.SetCurrentRelease(ctx, a.ID, &r.ID)
		}
		if crashed {
			now := int64(1)
			st.SetCrashed(ctx, a.ID, &now)
		}
	}
	add("rodando", "running", "node", 20001, false)
	add("parado", "stopped", "node", 20002, false)
	add("semrelease", "running", "", 20003, false)
	add("quebrado", "running", "node", 20004, true)
	add("site", "running", "static", 20005, false)
	sd := "dist"
	a, _ := st.AppByName(ctx, "site")
	a.StaticDir = &sd
	st.UpdateApp(ctx, a)

	in, err := BuildInput(ctx, st, cfg, "", LoadPages(""))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]AppRoute{}
	for _, r := range in.Apps {
		got[r.Name] = r
	}
	for name, kind := range map[string]Kind{"rodando": Process, "parado": Stopped, "semrelease": Stopped, "quebrado": Stopped, "site": Static} {
		if got[name].Kind != kind {
			t.Errorf("%s: %s, esperado %s", name, got[name].Kind, kind)
		}
	}
	if got["rodando"].Port != 20001 || got["site"].Root != "/h/apps/site/current/dist" {
		t.Fatalf("%+v %+v", got["rodando"], got["site"])
	}
	if cfgBytes, err := Generate(in); err != nil || !json.Valid(cfgBytes) {
		t.Fatal("config gerada inválida")
	}
}

func TestCaddySpecAndConfigFile(t *testing.T) {
	cfg := config.Default()
	cfg.SetHome(t.TempDir())
	spec := CaddySpec(cfg)
	if spec.Name != "dp-caddy" || !strings.Contains(spec.Run, "exec caddy run --config "+cfg.Dir("proxy", "caddy.json")) ||
		!strings.Contains(spec.Run, "XDG_DATA_HOME") || spec.Down {
		t.Fatalf("%+v", spec)
	}
	if err := WriteConfigFile(cfg, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg.Dir("proxy", "caddy.json")); string(b) != "{}" {
		t.Fatal("conteúdo")
	}
}

func TestLoadPagesOverride(t *testing.T) {
	d := t.TempDir()
	if p := LoadPages(d); !strings.Contains(p.NotFound, "404") || !strings.Contains(p.Unavailable, "503") {
		t.Fatal("páginas embutidas")
	}
	os.WriteFile(filepath.Join(d, "404.html"), []byte("meu 404"), 0o644)
	if p := LoadPages(d); p.NotFound != "meu 404" || !strings.Contains(p.Unavailable, "503") {
		t.Fatalf("%+v", p)
	}
}
