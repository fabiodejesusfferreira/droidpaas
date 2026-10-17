// Package web é o painel: HTML renderizado no servidor (html/template) + htmx vendorizado, sem CDN (TSD §12).
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"droidpaas/internal/api"
	"droidpaas/internal/stats"
	"droidpaas/internal/store"
	webfs "droidpaas/web"
)

type Server struct {
	API   *api.Server
	Stats *stats.Collector
	Log   *slog.Logger
	Now   func() time.Time
	// Sleep atrasa respostas de login falho (atraso progressivo); os testes trocam por um no-op.
	Sleep func(time.Duration)

	pages    map[string]*template.Template
	partials *template.Template
	static   http.Handler
	lim      *limiter
	limIP    *limiter

	mu      sync.Mutex
	flashes map[string][]Flash // por sessão
	secrets map[string]string  // valor exibido uma única vez (token novo), por sessão
}

type Flash struct{ Kind, Msg string }

// View é o que todo template de página recebe.
type View struct {
	Title, Nav string
	Bare       bool // sem sidebar (login)
	User, CSRF string
	Version    string
	Flash      []Flash
	Secret     string
	Data       any
}

var pageFiles = []string{"login", "error", "dashboard", "app_new", "app_publish", "app_overview", "app_deployments", "app_deployment",
	"app_env", "app_logs", "app_domains", "app_settings", "settings", "events"}

func New(a *api.Server, c *stats.Collector, log *slog.Logger) (*Server, error) {
	s := &Server{API: a, Stats: c, Log: log, Now: time.Now, Sleep: time.Sleep,
		lim: newLimiter(5, 5*time.Minute), limIP: newLimiter(20, 5*time.Minute),
		flashes: map[string][]Flash{}, secrets: map[string]string{}, pages: map[string]*template.Template{}}
	for _, p := range pageFiles {
		t, err := template.New("base.html").Funcs(s.funcs()).ParseFS(webfs.FS, "templates/base.html", "templates/partials/*.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", p, err)
		}
		s.pages[p] = t
	}
	pt, err := template.New("partials").Funcs(s.funcs()).ParseFS(webfs.FS, "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	s.partials = pt
	sub, _ := fs.Sub(webfs.FS, "static")
	s.static = http.StripPrefix("/static/", http.FileServerFS(sub))
	return s, nil
}

// Routes registra o painel no mesmo mux da API.
func (s *Server) Routes(mux *http.ServeMux) {
	pub := func(h func(*ctx)) http.HandlerFunc { return s.public(h) }
	au := func(h func(*ctx)) http.HandlerFunc { return s.authed(h) }

	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		s.securityHeaders(w)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		s.static.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /login", pub(s.loginForm))
	mux.HandleFunc("POST /login", pub(s.loginSubmit))
	mux.HandleFunc("POST /logout", au(s.logout))

	mux.HandleFunc("GET /{$}", au(s.dashboard))
	mux.HandleFunc("GET /fragments/apps", au(s.fragApps))
	mux.HandleFunc("GET /fragments/device", au(s.fragDevice))

	mux.HandleFunc("GET /apps/new", au(s.wizard))
	mux.HandleFunc("POST /apps/new", au(s.wizardSubmit))
	mux.HandleFunc("GET /apps/{name}", au(s.appOverview))
	mux.HandleFunc("GET /apps/{name}/fragments/stats", au(s.fragAppStats))
	mux.HandleFunc("GET /apps/{name}/publish", au(s.appPublish))
	mux.HandleFunc("POST /apps/{name}/action/{action}", au(s.appAction))

	mux.HandleFunc("GET /apps/{name}/deployments", au(s.deployments))
	mux.HandleFunc("POST /apps/{name}/deployments", au(s.deployNow))
	mux.HandleFunc("POST /apps/{name}/rollback", au(s.rollbackNow))
	mux.HandleFunc("GET /apps/{name}/deployments/{id}", au(s.deployment))
	mux.HandleFunc("GET /apps/{name}/deployments/{id}/stream", au(s.deploymentStream))
	mux.HandleFunc("POST /apps/{name}/deployments/{id}/cancel", au(s.deploymentCancel))

	mux.HandleFunc("GET /apps/{name}/environment", au(s.envPage))
	mux.HandleFunc("POST /apps/{name}/environment", au(s.envSave))
	mux.HandleFunc("POST /apps/{name}/environment/reveal", au(s.envReveal))

	mux.HandleFunc("GET /apps/{name}/logs", au(s.logsPage))
	mux.HandleFunc("GET /apps/{name}/logs/stream", au(s.logsStream))
	mux.HandleFunc("GET /apps/{name}/logs/download", au(s.logsDownload))

	mux.HandleFunc("GET /apps/{name}/domains", au(s.domainsPage))
	mux.HandleFunc("POST /apps/{name}/domains", au(s.domainAdd))
	mux.HandleFunc("POST /apps/{name}/domains/remove", au(s.domainRemove))

	mux.HandleFunc("GET /apps/{name}/settings", au(s.appSettings))
	mux.HandleFunc("POST /apps/{name}/settings", au(s.appSettingsSave))
	mux.HandleFunc("POST /apps/{name}/delete", au(s.appDelete))

	mux.HandleFunc("GET /settings", au(s.settingsPage))
	mux.HandleFunc("GET /settings/{tab}", au(s.settingsPage))
	mux.HandleFunc("POST /settings/general", au(s.settingsGeneral))
	mux.HandleFunc("POST /settings/tunnel", au(s.settingsTunnel))
	mux.HandleFunc("POST /settings/keys", au(s.keyAdd))
	mux.HandleFunc("POST /settings/keys/remove", au(s.keyRemove))
	mux.HandleFunc("POST /settings/tokens", au(s.tokenCreate))
	mux.HandleFunc("POST /settings/tokens/revoke", au(s.tokenRevoke))
	mux.HandleFunc("POST /settings/account", au(s.accountPassword))

	mux.HandleFunc("GET /events", au(s.eventsPage))
	mux.HandleFunc("/", pub(s.notFound))
}

// ---- cabeçalhos de segurança (TSD §12.6) ----

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func (s *Server) securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

// ---- contexto de requisição ----

type ctx struct {
	s    *Server
	w    http.ResponseWriter
	r    *http.Request
	sess *session
}

func (s *Server) public(h func(*ctx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.securityHeaders(w)
		w.Header().Set("Cache-Control", "no-store")
		h(&ctx{s: s, w: w, r: r})
	}
}

// authed exige sessão válida e, em métodos que alteram estado, o token CSRF (AT-20, AT-21).
func (s *Server) authed(h func(*ctx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.securityHeaders(w)
		w.Header().Set("Cache-Control", "no-store")
		sess, ok := s.sessionFrom(r)
		c := &ctx{s: s, w: w, r: r, sess: sess}
		if !ok {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next := ""
			if r.Method == http.MethodGet {
				next = "?next=" + urlEscape(r.URL.RequestURI())
			}
			http.Redirect(w, r, "/login"+next, http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if !s.csrfOK(r, sess) {
				s.API.Events.Warn(r.Context(), "auth.csrf_rejected", nil, "requisição recusada por CSRF: "+r.Method+" "+r.URL.Path, nil)
				c.fail(http.StatusForbidden, "err.csrf")
				return
			}
		}
		h(c)
	}
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (c *ctx) view(title, nav string) View {
	v := View{Title: title, Nav: nav, Version: c.s.API.Version}
	if c.sess != nil {
		v.User, v.CSRF = c.sess.Username, c.sess.CSRF
		v.Flash, v.Secret = c.s.takeFlash(c.sess.ID)
	}
	return v
}

func (c *ctx) render(code int, page string, v View) {
	t, ok := c.s.pages[page]
	if !ok {
		http.Error(c.w, "página inexistente", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer // renderiza antes de escrever o status: erro de template vira 500, não página pela metade
	if err := t.ExecuteTemplate(&buf, "base.html", v); err != nil {
		c.s.Log.Error("render", "page", page, "err", err)
		http.Error(c.w, "erro interno", http.StatusInternalServerError)
		return
	}
	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.w.WriteHeader(code)
	c.w.Write(buf.Bytes())
}

func (c *ctx) fragment(name string, data any) {
	var buf bytes.Buffer
	if err := c.s.partials.ExecuteTemplate(&buf, name, data); err != nil {
		c.s.Log.Error("fragmento", "name", name, "err", err)
		http.Error(c.w, "erro interno", http.StatusInternalServerError)
		return
	}
	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.w.Write(buf.Bytes())
}

func (c *ctx) fail(code int, msgKey string) {
	v := c.view(msgFor(code), "")
	v.Bare = c.sess == nil
	v.Data = struct {
		Code int
		Msg  string
	}{code, msgKey}
	c.render(code, "error", v)
}

func msgFor(code int) string {
	switch code {
	case 403:
		return T("err.forbidden.title")
	case 404:
		return T("err.notfound.title")
	}
	return T("err.generic.title")
}

func (c *ctx) redirect(to string) { http.Redirect(c.w, c.r, to, http.StatusSeeOther) }

// flash guarda uma mensagem para a próxima página renderizada da sessão (padrão PRG).
func (c *ctx) flash(kind, key string, args ...any) {
	c.s.addFlash(c.sess.ID, Flash{Kind: kind, Msg: fmt.Sprintf(T(key), args...)})
}

func (c *ctx) ok(key string, args ...any)  { c.flash("ok", key, args...) }
func (c *ctx) err(key string, args ...any) { c.flash("err", key, args...) }

func (s *Server) addFlash(sid string, f Flash) {
	s.mu.Lock()
	s.flashes[sid] = append(s.flashes[sid], f)
	s.mu.Unlock()
}

func (s *Server) takeFlash(sid string) ([]Flash, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, sec := s.flashes[sid], s.secrets[sid]
	delete(s.flashes, sid)
	delete(s.secrets, sid)
	return f, sec
}

func (s *Server) showOnce(sid, secret string) {
	s.mu.Lock()
	s.secrets[sid] = secret
	s.mu.Unlock()
}

func (c *ctx) notFoundApp() { c.fail(http.StatusNotFound, "err.app_not_found") }

func (c *ctx) app() (store.App, bool) {
	a, err := c.s.API.Apps.Get(c.r.Context(), c.r.PathValue("name"))
	if err != nil {
		c.notFoundApp()
		return a, false
	}
	return a, true
}

func (s *Server) notFound(c *ctx) {
	if c.r.URL.Path != "/" && strings.HasPrefix(c.r.URL.Path, "/api/") {
		http.NotFound(c.w, c.r)
		return
	}
	sess, ok := s.sessionFrom(c.r)
	if ok {
		c.sess = sess
	}
	c.fail(http.StatusNotFound, "err.notfound")
}

func (c *ctx) fragmentTo(name string, data any) {
	if err := c.s.partials.ExecuteTemplate(c.w, name, data); err != nil {
		c.s.Log.Error("fragmento", "name", name, "err", err)
	}
}

func itoa(n int) string { return fmt.Sprint(n) }

func jsonLine(ts, text string) string {
	b, _ := json.Marshal(map[string]string{"ts": ts, "text": text})
	return string(b)
}
