// Package ghsrc publica apps a partir do GitHub: um webhook de push (assinado) dispara `git fetch` do repositório
// para o repo.git do app e enfileira um deploy normal. Repositórios privados usam uma deploy key só de leitura.
package ghsrc

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/deploy"
	"droidpaas/internal/events"
	"droidpaas/internal/store"
)

var (
	ErrURL    = errors.New("endereço inválido: use https://github.com/dono/repo ou git@github.com:dono/repo.git")
	ErrBranch = errors.New("nome de branch inválido")
	ErrNone   = errors.New("este app não está ligado a um repositório do GitHub")
	ErrFetch  = errors.New("não consegui buscar o código no GitHub")

	slugRe   = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9_][A-Za-z0-9_.-]{0,99})$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
)

type Service struct {
	Store  *store.Store
	Box    *crypt.Box
	Deploy *deploy.Engine
	Cfg    config.Config
	Events *events.Log
	Log    *slog.Logger

	mu    sync.Mutex
	locks map[int64]*sync.Mutex // um fetch+deploy por app de cada vez
	seen  map[string]time.Time  // X-GitHub-Delivery já tratados (o GitHub reenvia)

	// Ganchos só para testes (nil em produção).
	Spawn func(func())                 // roda o sync do webhook de forma síncrona
	Remap func(fetchURL string) string // troca o GitHub por um repositório local
}

// Info é o que a API e o painel mostram (nunca a chave privada; o segredo só por Secret).
type Info struct {
	URL       string `json:"url"`
	Branch    string `json:"branch"`
	Private   bool   `json:"private"`
	DeployPub string `json:"deploy_key,omitempty"`
	LastSync  *int64 `json:"last_sync_at"`
	LastError string `json:"last_error,omitempty"`
}

// ParseURL aceita só github.com (https, ou ssh para repositório privado) e devolve a URL de fetch, se exige deploy key e "dono/repo".
// Qualquer outra forma (file://, ext::, --opções, outros hosts) é recusada: a URL vai para o `git fetch`.
func ParseURL(raw string) (fetchURL string, private bool, slug string, err error) {
	raw = strings.TrimSpace(raw)
	var rest string
	switch {
	case strings.HasPrefix(raw, "https://github.com/"):
		rest = strings.TrimPrefix(raw, "https://github.com/")
	case strings.HasPrefix(raw, "git@github.com:"):
		rest, private = strings.TrimPrefix(raw, "git@github.com:"), true
	case strings.HasPrefix(raw, "ssh://git@github.com/"):
		rest, private = strings.TrimPrefix(raw, "ssh://git@github.com/"), true
	default:
		return "", false, "", ErrURL
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	m := slugRe.FindStringSubmatch(rest)
	if m == nil || m[2] == "." || m[2] == ".." {
		return "", false, "", ErrURL
	}
	slug = m[1] + "/" + m[2]
	if private {
		return "ssh://git@github.com/" + slug + ".git", true, slug, nil
	}
	return "https://github.com/" + slug + ".git", false, slug, nil
}

func ValidBranch(b string) bool {
	return branchRe.MatchString(b) && !strings.Contains(b, "..") && !strings.Contains(b, "//") && !strings.HasSuffix(b, "/") && !strings.HasSuffix(b, ".lock")
}

// VerifySignature confere o cabeçalho X-Hub-Signature-256 ("sha256=<hex>") em tempo constante.
func VerifySignature(secret, body []byte, header string) bool {
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hmac.Equal(m.Sum(nil), got)
}

// Connect liga (ou religa) o app a um repositório, gerando segredo novo e, se for SSH, uma deploy key nova.
func (s *Service) Connect(ctx context.Context, app store.App, rawURL, branch string) (Info, error) {
	fetchURL, private, slug, err := ParseURL(rawURL)
	if err != nil {
		return Info{}, err
	}
	if branch == "" {
		branch = app.Branch
	}
	if !ValidBranch(branch) {
		return Info{}, ErrBranch
	}
	secret, err := randHex(32)
	if err != nil {
		return Info{}, err
	}
	sealed, err := s.Box.Seal([]byte(secret))
	if err != nil {
		return Info{}, err
	}
	g := store.GitHubSource{AppID: app.ID, URL: fetchURL, Branch: branch, Secret: sealed}
	if private {
		pemKey, pub, err := newDeployKey("droidpaas-" + app.Name)
		if err != nil {
			return Info{}, err
		}
		if g.DeployKey, err = s.Box.Seal(pemKey); err != nil {
			return Info{}, err
		}
		g.DeployPub = pub
	}
	if err := s.Store.SetGitHubSource(ctx, g); err != nil {
		return Info{}, err
	}
	s.Events.Info(ctx, "github.connected", &app.ID, "app ligado a github.com/"+slug+" ("+branch+")", nil)
	return info(g), nil
}

func (s *Service) Get(ctx context.Context, app store.App) (Info, error) {
	g, err := s.Store.GitHubSource(ctx, app.ID)
	if errors.Is(err, store.ErrNotFound) {
		return Info{}, ErrNone
	}
	return info(g), err
}

func (s *Service) Secret(ctx context.Context, app store.App) (string, error) {
	g, err := s.Store.GitHubSource(ctx, app.ID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNone
	}
	if err != nil {
		return "", err
	}
	b, err := s.Box.Open(g.Secret)
	return string(b), err
}

func (s *Service) RotateSecret(ctx context.Context, app store.App) (string, error) {
	if _, err := s.Store.GitHubSource(ctx, app.ID); err != nil {
		return "", ErrNone
	}
	secret, err := randHex(32)
	if err != nil {
		return "", err
	}
	sealed, err := s.Box.Seal([]byte(secret))
	if err != nil {
		return "", err
	}
	s.Events.Info(ctx, "github.secret_rotated", &app.ID, "segredo do webhook trocado", nil)
	return secret, s.Store.SetGitHubSecret(ctx, app.ID, sealed)
}

func (s *Service) Disconnect(ctx context.Context, app store.App) error {
	if _, err := s.Store.GitHubSource(ctx, app.ID); err != nil {
		return ErrNone
	}
	s.Events.Info(ctx, "github.disconnected", &app.ID, "app desligado do GitHub", nil)
	return s.Store.DeleteGitHubSource(ctx, app.ID)
}

func info(g store.GitHubSource) Info {
	return Info{URL: g.URL, Branch: g.Branch, Private: g.DeployKey != nil, DeployPub: g.DeployPub, LastSync: g.LastSyncAt, LastError: g.LastError}
}

// Sync busca a branch no GitHub e enfileira o deploy do commit que chegou.
func (s *Service) Sync(ctx context.Context, app store.App) (store.Deploy, error) {
	l := s.lock(app.ID)
	l.Lock()
	defer l.Unlock()
	var d store.Deploy
	g, err := s.Store.GitHubSource(ctx, app.ID)
	if errors.Is(err, store.ErrNotFound) {
		return d, ErrNone
	}
	if err != nil {
		return d, err
	}
	fail := func(err error) (store.Deploy, error) {
		s.Store.SetGitHubSync(context.Background(), app.ID, err.Error())
		s.Events.Warn(context.Background(), "github.sync_failed", &app.ID, "falha ao buscar do GitHub: "+err.Error(), nil)
		return d, err
	}
	if err := s.fetch(ctx, app, g); err != nil {
		return fail(err)
	}
	sha, err := s.Deploy.Git.RevParse(ctx, s.Deploy.RepoPath(app.Name), "refs/heads/"+g.Branch)
	if err != nil {
		return fail(fmt.Errorf("a branch %q não existe no repositório: %w", g.Branch, err))
	}
	if d, err = s.Deploy.Enqueue(ctx, app.Name, "github", deploy.EnqueueOpts{SHA: sha, Ref: g.Branch}); err != nil {
		return fail(err)
	}
	s.Store.SetGitHubSync(ctx, app.ID, "")
	return d, nil
}

func (s *Service) fetch(ctx context.Context, app store.App, g store.GitHubSource) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	url := g.URL
	protos := "https:ssh"
	if s.Remap != nil {
		url, protos = s.Remap(url), protos+":file"
	}
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=" + protos}
	if g.DeployKey != nil {
		pemKey, err := s.Box.Open(g.DeployKey)
		if err != nil {
			return err
		}
		tmp := s.Cfg.Dir("tmp")
		os.MkdirAll(tmp, 0o700)
		f, err := os.CreateTemp(tmp, "ghkey-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		f.Chmod(0o600)
		f.Write(pemKey)
		f.Close()
		known := filepath.Join(s.Cfg.Dir("etc"), "github_known_hosts")
		env = append(env, "GIT_SSH_COMMAND=ssh -i '"+f.Name()+"' -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile='"+known+"'")
	}
	refspec := "+refs/heads/" + g.Branch + ":refs/heads/" + g.Branch
	if out, err := realRun(ctx, s.Deploy.RepoPath(app.Name), env, "git", "fetch", "--no-tags", "--quiet", "--", url, refspec); err != nil {
		if out == "" {
			out = err.Error()
		}
		if len(out) > 300 {
			out = out[:300]
		}
		return fmt.Errorf("%w: %s", ErrFetch, out)
	}
	return nil
}

func realRun(ctx context.Context, dir string, env []string, name string, a ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, a...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	b, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(b)), err
}

// Webhook trata uma entrega do GitHub e devolve o status HTTP e uma frase curta. Assinatura inválida, app inexistente e
// app sem GitHub respondem igual (401), para não revelar quais apps existem. O fetch+deploy roda em segundo plano:
// o GitHub desiste de uma entrega após 10 s.
func (s *Service) Webhook(ctx context.Context, appName, event, delivery, signature string, body []byte) (int, string) {
	app, err := s.Store.AppByName(ctx, appName)
	var g store.GitHubSource
	var secret []byte
	if err == nil {
		if g, err = s.Store.GitHubSource(ctx, app.ID); err == nil {
			secret, err = s.Box.Open(g.Secret)
		}
	}
	if err != nil {
		VerifySignature([]byte("sem-segredo"), body, signature) // mesmo custo, para não vazar por tempo
		return 401, "assinatura inválida"
	}
	if !VerifySignature(secret, body, signature) {
		return 401, "assinatura inválida"
	}
	switch event {
	case "ping":
		return 200, "pong"
	case "push":
	default:
		return 202, "evento ignorado"
	}
	if s.duplicate(delivery) {
		return 200, "entrega já tratada"
	}
	var p struct {
		Ref        string `json:"ref"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &p) != nil {
		return 400, "corpo inválido"
	}
	_, _, slug, _ := ParseURL(g.URL)
	if p.Deleted || p.Ref != "refs/heads/"+g.Branch {
		return 202, "ignorado: não é um push na branch " + g.Branch
	}
	if !strings.EqualFold(p.Repository.FullName, slug) {
		return 202, "ignorado: repositório diferente do configurado"
	}
	spawn := s.Spawn
	if spawn == nil {
		spawn = func(f func()) { go f() }
	}
	spawn(func() {
		bg, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if _, err := s.Sync(bg, app); err != nil && s.Log != nil {
			s.Log.Warn("github: sync do webhook falhou", "app", app.Name, "erro", err)
		}
	})
	return 202, "deploy agendado"
}

func (s *Service) lock(id int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	if s.locks[id] == nil {
		s.locks[id] = &sync.Mutex{}
	}
	return s.locks[id]
}

func (s *Service) duplicate(delivery string) bool {
	if delivery == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	now := time.Now()
	if len(s.seen) > 256 {
		for k, t := range s.seen {
			if now.Sub(t) > time.Hour {
				delete(s.seen, k)
			}
		}
	}
	if _, ok := s.seen[delivery]; ok {
		return true
	}
	s.seen[delivery] = now
	return false
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newDeployKey gera um par ed25519: a chave privada em PEM OpenSSH e a pública no formato de authorized_keys.
func newDeployKey(comment string) (privPEM []byte, pub string, err error) {
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	blk, err := ssh.MarshalPrivateKey(sk, comment)
	if err != nil {
		return nil, "", err
	}
	sp, err := ssh.NewPublicKey(pk)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(blk), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment, nil
}
