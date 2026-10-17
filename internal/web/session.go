package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"droidpaas/internal/store"
)

const (
	cookieSession = "dp_session"
	cookieLogin   = "dp_login_csrf"
	idleTimeout   = 12 * time.Hour
	absTimeout    = 7 * 24 * time.Hour
)

type session struct {
	ID       string // SHA-256 do token do cookie (é o que o banco guarda)
	UserID   int64
	Username string
	CSRF     string
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // sem entropia não dá para operar com segurança
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// ---- cliente, HTTPS e cookies ----

// trustedPeer: o par TCP é um proxy confiável (cloudflared/Caddy locais), cujos cabeçalhos X-Forwarded-* valem.
func (s *Server) trustedPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	for _, t := range s.API.Cfg.HTTP.TrustedProxies {
		if t == host {
			return true
		}
	}
	return false
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.trustedPeer(r) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	return host
}

func (s *Server) secureRequest(r *http.Request) bool {
	switch s.API.Cfg.HTTP.SecureCookies {
	case "always":
		return true
	case "never":
		return false
	}
	if r.TLS != nil {
		return true
	}
	return s.trustedPeer(r) && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, name, val string, maxAge time.Duration, strict bool) {
	c := &http.Cookie{Name: name, Value: val, Path: "/", HttpOnly: true, Secure: s.secureRequest(r), SameSite: http.SameSiteLaxMode}
	if strict {
		c.SameSite = http.SameSiteStrictMode
	}
	if maxAge > 0 {
		c.MaxAge = int(maxAge.Seconds())
	} else if maxAge < 0 {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// ---- sessões ----

func (s *Server) newSession(r *http.Request, userID int64) (token string, err error) {
	token = randToken(32)
	now := s.Now().UnixMilli()
	err = s.API.Store.CreateSession(r.Context(), store.Session{
		ID: hashToken(token), UserID: userID, CSRFSecret: randToken(32), CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now + absTimeout.Milliseconds(), UserAgent: trunc(r.UserAgent(), 200), IP: s.clientIP(r),
	})
	return
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// sessionFrom valida o cookie: 12 h de inatividade e 7 dias absolutos (TSD §13.2). Sessão vencida é apagada.
func (s *Server) sessionFrom(r *http.Request) (*session, bool) {
	ck, err := r.Cookie(cookieSession)
	if err != nil || ck.Value == "" {
		return nil, false
	}
	id := hashToken(ck.Value)
	x, err := s.API.Store.SessionByID(r.Context(), id)
	if err != nil {
		return nil, false
	}
	now := s.Now().UnixMilli()
	if now >= x.ExpiresAt || now-x.LastSeenAt >= idleTimeout.Milliseconds() {
		s.API.Store.DeleteSession(r.Context(), id)
		return nil, false
	}
	if now-x.LastSeenAt > time.Minute.Milliseconds() { // não escreve no banco a cada requisição
		s.API.Store.TouchSession(r.Context(), id, now)
	}
	name, err := s.API.Store.User(r.Context(), x.UserID)
	if err != nil {
		return nil, false
	}
	return &session{ID: id, UserID: x.UserID, Username: name, CSRF: x.CSRFSecret}, true
}

// ---- CSRF (TSD §12.6): token por sessão + verificação de Origin/Referer ----

func (s *Server) csrfOK(r *http.Request, sess *session) bool {
	tok := r.Header.Get("X-CSRF-Token")
	if tok == "" {
		tok = r.PostFormValue("_csrf")
	}
	return tok != "" && eq(tok, sess.CSRF) && s.sameOrigin(r)
}

// sameOrigin: se vier Origin (ou Referer), o host precisa ser o do painel. Sem nenhum dos dois, o token decide.
func (s *Server) sameOrigin(r *http.Request) bool {
	host := r.Host
	if s.trustedPeer(r) {
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			host = strings.TrimSpace(strings.Split(h, ",")[0])
		}
	}
	for _, h := range []string{"Origin", "Referer"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		if v == "null" {
			return false
		}
		u, err := url.Parse(v)
		if err != nil || u.Host == "" {
			return false
		}
		return strings.EqualFold(u.Host, host)
	}
	return true
}

// ---- limite de tentativas de login (TSD §13.1 T-02) ----

type limiter struct {
	mu     sync.Mutex
	fails  map[string][]time.Time
	max    int
	window time.Duration
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{fails: map[string][]time.Time{}, max: max, window: window}
}

func (l *limiter) prune(key string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range l.fails[key] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = keep
	}
	return keep
}

// blocked diz se a chave estourou o limite e por quanto tempo ainda.
func (l *limiter) blocked(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.prune(key, now)
	if len(f) < l.max {
		return false, 0
	}
	return true, l.window - now.Sub(f[len(f)-l.max])
}

func (l *limiter) fail(key string, now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.prune(key, now), now)
	return len(l.fails[key])
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
