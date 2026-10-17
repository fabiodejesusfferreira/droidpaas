package web

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"droidpaas/internal/auth"
)

type loginData struct {
	CSRF, Next, Username string
	Err                  string
}

func (c *ctx) loginPage(code int, d loginData) {
	tok := randToken(24)
	c.s.setCookie(c.w, c.r, cookieLogin, tok, 30*time.Minute, true)
	d.CSRF = tok
	v := c.view(T("login.title"), "")
	v.Bare, v.Data = true, d
	c.render(code, "login", v)
}

func (s *Server) loginForm(c *ctx) {
	if _, ok := s.sessionFrom(c.r); ok {
		c.redirect("/")
		return
	}
	c.loginPage(http.StatusOK, loginData{Next: safeNext(c.r.URL.Query().Get("next"))})
}

func (s *Server) loginSubmit(c *ctx) {
	r := c.r
	r.Body = http.MaxBytesReader(c.w, r.Body, 1<<16)
	if err := r.ParseForm(); err != nil || !s.sameOrigin(r) {
		c.fail(http.StatusForbidden, "err.csrf")
		return
	}
	ck, err := r.Cookie(cookieLogin)
	if err != nil || ck.Value == "" || !eq(ck.Value, r.PostFormValue("_csrf")) { // double-submit antes de existir sessão
		c.fail(http.StatusForbidden, "err.csrf")
		return
	}
	user := strings.TrimSpace(r.PostFormValue("username"))
	pass := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))
	ip, now := s.clientIP(r), s.Now()
	keyU, keyI := "u:"+ip+"|"+strings.ToLower(user), "ip:"+ip

	if b, wait := s.lim.blocked(keyU, now); b {
		c.tooMany(wait, user, next)
		return
	}
	if b, wait := s.limIP.blocked(keyI, now); b {
		c.tooMany(wait, user, next)
		return
	}

	id, hash, lookupErr := s.API.Store.UserByName(r.Context(), user)
	if lookupErr != nil {
		hash = s.dummyHash() // custo igual para usuário inexistente: não revela quem existe
	}
	if !auth.VerifyPassword(pass, hash) || lookupErr != nil {
		n := s.lim.fail(keyU, now)
		s.limIP.fail(keyI, now)
		s.Sleep(min(time.Duration(n)*300*time.Millisecond, 2*time.Second)) // atraso progressivo
		s.API.Events.Warn(r.Context(), "auth.login_failed", nil, "login falhou para o usuário "+strconv.Quote(trunc(SanitizeLine(user), 40)),
			map[string]any{"ip": ip, "tentativas": n})
		c.loginPage(http.StatusUnauthorized, loginData{Next: next, Username: user, Err: "login.invalid"})
		return
	}
	s.lim.reset(keyU)
	tok, err := s.newSession(r, id)
	if err != nil {
		s.Log.Error("criar sessão", "err", err)
		c.fail(http.StatusInternalServerError, "err.generic")
		return
	}
	s.setCookie(c.w, r, cookieSession, tok, absTimeout, false)
	s.setCookie(c.w, r, cookieLogin, "", -1, true)
	s.API.Events.Info(r.Context(), "auth.login", nil, "login de "+strconv.Quote(trunc(SanitizeLine(user), 40)), map[string]any{"ip": ip})
	c.redirect(next)
}

func (c *ctx) tooMany(wait time.Duration, user, next string) {
	c.w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	c.loginPage(http.StatusTooManyRequests, loginData{Next: next, Username: user, Err: "login.toomany"})
}

func (s *Server) logout(c *ctx) {
	s.API.Store.DeleteSession(c.r.Context(), c.sess.ID)
	s.API.Events.Info(c.r.Context(), "auth.logout", nil, "logout", nil)
	s.setCookie(c.w, c.r, cookieSession, "", -1, false)
	c.redirect("/login")
}

var (
	dummyOnce sync.Once
	dummyH    string
)

func (s *Server) dummyHash() string {
	dummyOnce.Do(func() { dummyH, _ = auth.HashPassword("senha-ficticia-para-igualar-o-tempo") })
	return dummyH
}
