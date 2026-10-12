// Package deploy orquestra build → troca → start → health → rollback (TSD §8.3, §9.3–9.5).
package deploy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"droidpaas/internal/apps"
	"droidpaas/internal/config"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
	"droidpaas/internal/system"
)

var (
	ErrBadRef     = errors.New("ref inexistente no repositório (faça um git push primeiro)")
	ErrNoPrevious = errors.New("não há release anterior para o rollback")
	ErrBadRelease = errors.New("release inválida para rollback")
	ErrTooLate    = errors.New("o deploy já passou do ponto de troca e não pode ser cancelado")
	ErrNotActive  = errors.New("deploy não está em andamento")
)

type Engine struct {
	Store  *store.Store
	Apps   *apps.Service
	Runit  runit.ServiceManager
	Git    system.GitRunner
	Runner system.CommandRunner
	Cfg    config.Config
	Events *events.Log
	Log    *slog.Logger
	// Health testa se o app respondeu; o padrão faz TCP ou GET HTTP em 127.0.0.1.
	Health func(ctx context.Context, port int, path string) error
	Wake   func() // acorda o reconciliador depois de mudar o estado desejado
	Now    func() time.Time

	hub     *Hub
	mu      sync.Mutex
	cancels map[int64]*cancelState
	active  map[int64]int64 // app id → deploy id em execução
	kick    chan struct{}
}

type cancelState struct {
	cancel   context.CancelFunc
	switched bool // depois da troca de release não dá mais para cancelar
	user     bool // o cancelamento veio do usuário (e não de timeout)
}

// New completa os campos internos de um Engine montado pelo chamador.
func New(e *Engine) *Engine {
	e.hub = newHub()
	e.cancels = map[int64]*cancelState{}
	e.active = map[int64]int64{}
	e.kick = make(chan struct{}, 1)
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Health == nil {
		e.Health = defaultHealth
	}
	return e
}

func (e *Engine) RepoPath(app string) string { return filepath.Join(e.Apps.AppDir(app), "repo.git") }
func (e *Engine) logDir(app string) string   { return e.Cfg.Dir("logs", "deploys", app) }

func (e *Engine) IsDeploying(appID int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.active[appID]
	return ok
}

func (e *Engine) wake() {
	if e.Wake != nil {
		e.Wake()
	}
}

func (e *Engine) poke() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

type EnqueueOpts struct {
	Ref       string // manual/rebuild: branch/tag/sha (padrão: branch do app)
	SHA       string // git_push: sha já recebido
	Rebuild   bool
	ReleaseID int64 // rollback: release alvo (0 = a anterior à atual)
}

// Enqueue valida, grava o deploy na fila persistente e acorda o worker.
func (e *Engine) Enqueue(ctx context.Context, appName, trigger string, o EnqueueOpts) (store.Deploy, error) {
	var d store.Deploy
	a, err := e.Store.AppByName(ctx, appName)
	if err != nil {
		return d, err
	}
	d = store.Deploy{AppID: a.ID, Trigger: trigger, Rebuild: o.Rebuild}
	switch trigger {
	case "git_push":
		d.GitSHA, d.GitRef = o.SHA, first(o.Ref, a.Branch)
	case "manual", "rebuild":
		d.Rebuild = d.Rebuild || trigger == "rebuild"
		d.GitRef = first(o.Ref, a.Branch)
		if d.GitSHA, err = e.Git.RevParse(ctx, e.RepoPath(a.Name), d.GitRef); err != nil {
			return d, fmt.Errorf("%w: %v", ErrBadRef, err)
		}
	case "rollback":
		rel, err := e.pickRollbackTarget(ctx, a, o.ReleaseID)
		if err != nil {
			return d, err
		}
		d.ReleaseID = &rel.ID
		d.GitSHA, d.GitRef = rel.GitSHA, rel.GitRef
	default:
		return d, fmt.Errorf("trigger inválido: %q", trigger)
	}
	if err := e.Store.InsertDeploy(ctx, &d); err != nil {
		return d, err
	}
	d.LogPath = filepath.Join(e.logDir(a.Name), strconv.FormatInt(d.ID, 10)+".log")
	if err := e.Store.SetDeployLogPath(ctx, d.ID, d.LogPath); err != nil {
		return d, err
	}
	e.Events.Info(ctx, "deploy.queued", &a.ID, fmt.Sprintf("deploy #%d na fila (%s)", d.ID, trigger), map[string]any{"deploy": d.ID, "sha": d.GitSHA})
	e.poke()
	return d, nil
}

func (e *Engine) pickRollbackTarget(ctx context.Context, a store.App, want int64) (store.Release, error) {
	if want != 0 {
		r, err := e.Store.Release(ctx, want)
		if err != nil || r.AppID != a.ID || r.Status != "ready" {
			return r, ErrBadRelease
		}
		if _, err := os.Stat(r.Path); err != nil {
			return r, ErrBadRelease
		}
		return r, nil
	}
	rels, err := e.Store.Releases(ctx, a.ID, "ready")
	if err != nil {
		return store.Release{}, err
	}
	for _, r := range rels {
		if a.CurrentReleaseID != nil && r.ID == *a.CurrentReleaseID {
			continue
		}
		if _, err := os.Stat(r.Path); err == nil {
			return r, nil
		}
	}
	return store.Release{}, ErrNoPrevious
}

// Cancel cancela um deploy na fila, ou em execução antes da troca de release.
func (e *Engine) Cancel(ctx context.Context, id int64) error {
	d, err := e.Store.Deploy(ctx, id)
	if err != nil {
		return err
	}
	if ok, err := e.Store.CancelQueuedDeploy(ctx, id); err != nil || ok {
		return err
	}
	e.mu.Lock()
	cs := e.cancels[id]
	e.mu.Unlock()
	if cs == nil || d.Status != "running" {
		return ErrNotActive
	}
	e.mu.Lock()
	switched := cs.switched
	if !switched {
		cs.user = true
	}
	e.mu.Unlock()
	if switched {
		return ErrTooLate
	}
	cs.cancel()
	return nil
}

// Run sobe os workers (build.max_concurrent) e bloqueia até ctx terminar.
func (e *Engine) Run(ctx context.Context) {
	e.Recover(ctx)
	var wg sync.WaitGroup
	for i := 0; i < max(e.Cfg.Build.MaxConcurrent, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.worker(ctx)
		}()
	}
	wg.Wait()
}

func (e *Engine) worker(ctx context.Context) {
	for ctx.Err() == nil {
		d, err := e.Store.ClaimNextDeploy(ctx)
		if err != nil {
			e.Log.Error("fila de deploys", "err", err)
		}
		if d == nil {
			select {
			case <-ctx.Done():
				return
			case <-e.kick:
			case <-time.After(2 * time.Second): // rede de segurança contra pokes perdidos entre workers
			}
			continue
		}
		e.execute(ctx, *d)
	}
}

// Recover limpa o que uma queda do droidd deixou pela metade (os deploys running viram interrupted no boot).
func (e *Engine) Recover(ctx context.Context) {
	paths, err := e.Store.FailBuildingReleases(ctx)
	if err != nil {
		e.Log.Error("recuperação de releases", "err", err)
		return
	}
	for _, p := range paths {
		if strings.Contains(p, string(filepath.Separator)+"releases"+string(filepath.Separator)) {
			os.RemoveAll(p)
		}
	}
}

// ---- acompanhamento de logs ----

// Follow entrega as linhas do log do deploy (arquivo + ao vivo) a partir da linha `from` (0 = início)
// e devolve o status final. emit recebe o número da linha (1-based) e o texto.
func (e *Engine) Follow(ctx context.Context, id int64, from int, emit func(n int, line string)) (string, error) {
	if err := e.WaitStarted(ctx, id); err != nil {
		return "", err
	}
	d, err := e.Store.Deploy(ctx, id)
	if err != nil {
		return "", err
	}
	// o status vira `running` um instante antes de o stream existir: espera o registro
	for i := 0; i < 100 && d.Status == "running" && e.hub.get(id) == nil; i++ {
		time.Sleep(20 * time.Millisecond)
		if d, err = e.Store.Deploy(ctx, id); err != nil {
			return "", err
		}
	}
	n := 0
	send := func(line string) {
		n++
		if n > from {
			emit(n, line)
		}
	}
	if st := e.hub.get(id); st != nil {
		replay, ch := st.subscribe()
		defer st.unsubscribe(ch)
		if b, err := readN(d.LogPath, replay); err == nil {
			scanLines(b, send)
		}
		for open := true; open; {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case line, ok := <-ch:
				if !ok {
					open = false
					break
				}
				send(line)
			}
		}
	} else if d.LogPath != "" {
		if b, err := os.ReadFile(d.LogPath); err == nil {
			scanLines(b, send)
		}
	}
	// o canal fecha quando o stream termina; o status final já foi gravado nesse momento
	for i := 0; i < 50; i++ {
		fin, err := e.Store.Deploy(ctx, id)
		if err != nil {
			return "", err
		}
		if fin.Status != "running" && fin.Status != "queued" {
			return fin.Status, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "running", nil
}

// WaitQueued bloqueia até o deploy sair de `queued` e começar (ou terminar), para quem quer seguir o log.
func (e *Engine) WaitStarted(ctx context.Context, id int64) error {
	for {
		d, err := e.Store.Deploy(ctx, id)
		if err != nil {
			return err
		}
		if d.Status != "queued" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func scanLines(b []byte, f func(string)) {
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		f(strings.TrimRight(sc.Text(), "\r"))
	}
}

// ---- health check ----

func defaultHealth(ctx context.Context, port int, path string) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if path == "" {
		c, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return c.Close()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return err
	}
	cli := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("GET %s → %s", path, resp.Status)
	}
	return nil
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// PruneLogs apaga logs de deploy mais antigos que build.keep_logs_days (TSD §14.1).
func (e *Engine) PruneLogs(ctx context.Context) error {
	days := e.Cfg.Build.KeepLogsDays
	if days <= 0 {
		return nil
	}
	old, err := e.Store.OldDeployLogs(ctx, e.Now().AddDate(0, 0, -days))
	if err != nil {
		return err
	}
	for id, p := range old {
		os.Remove(p)
		e.Store.ClearDeployLogPath(ctx, id)
	}
	return nil
}
