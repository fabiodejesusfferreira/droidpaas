package api

import (
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"droidpaas/internal/auth"
)

var gitPathRe = regexp.MustCompile(`^/git/([a-z][a-z0-9-]{1,30})\.git(/.*)?$`)

// registerGitHTTP expõe o Git smart HTTP (TSD §8.4.3, RF-05) delegando a `git http-backend` via CGI. A autenticação é
// HTTP Basic: o usuário é ignorado e a senha é um token de API com escopo `git` ou `full`.
func (s *Server) registerGitHTTP(mux *http.ServeMux) {
	mux.HandleFunc("/git/", s.handleGitHTTP)
}

func (s *Server) gitHTTPEnabled(r *http.Request) bool {
	set, err := s.EffectiveSettings(r.Context())
	return err == nil && set["git_http_enabled"] == "true"
}

func (s *Server) handleGitHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.gitHTTPEnabled(r) {
		http.NotFound(w, r)
		return
	}
	m := gitPathRe.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	_, pass, ok := r.BasicAuth()
	var name string
	if ok && strings.HasPrefix(pass, "dp_") {
		if t, err := s.Store.TokenByHash(r.Context(), auth.HashToken(pass)); err == nil && (t.Scope == "git" || t.Scope == "full") {
			name = t.Name
			s.Store.TouchToken(r.Context(), t.ID)
		}
	}
	if name == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="DroidPaaS"`)
		http.Error(w, "autenticação necessária (use um token de API como senha)", http.StatusUnauthorized)
		return
	}
	app, rest := m[1], m[2]
	repo := filepath.Join(s.Apps.AppDir(app), "repo.git")
	if fi, err := os.Stat(repo); err != nil || !fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		http.Error(w, "git indisponível", http.StatusInternalServerError)
		return
	}
	// o http-backend enxerga <apps>/<app>/repo.git; reescreve o caminho público /git/<app>.git/… para isso
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + app + "/repo.git" + rest
	r2.URL.RawPath = ""
	h := &cgi.Handler{
		Path: gitBin,
		Args: []string{"http-backend"},
		Env: []string{
			"GIT_PROJECT_ROOT=" + s.Cfg.Dir("apps"),
			"GIT_HTTP_EXPORT_ALL=1",
			"REMOTE_USER=" + name,
			"GIT_COMMITTER_NAME=DroidPaaS", "GIT_COMMITTER_EMAIL=droidpaas@localhost",
			"PATH=" + os.Getenv("PATH"),
		},
		InheritEnv: []string{"HOME", "PREFIX", "LD_PRELOAD", "TMPDIR"},
		Stderr:     nil,
	}
	h.ServeHTTP(w, r2)
}
