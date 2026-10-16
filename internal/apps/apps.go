// Package apps reúne as regras de negócio dos apps: validação, portas, estado desejado, domínios e env (TSD §8.7–8.9, §9.2).
package apps

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"droidpaas/internal/config"
	"droidpaas/internal/crypt"
	"droidpaas/internal/events"
	"droidpaas/internal/runit"
	"droidpaas/internal/store"
)

var (
	ErrConflict  = errors.New("conflito")
	ErrNoRelease = errors.New("o app ainda não tem release; faça um deploy primeiro")
)

// ValidationError aponta o campo inválido (vira 422 na API).
type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

func invalid(field, format string, a ...any) error {
	return &ValidationError{field, fmt.Sprintf(format, a...)}
}

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
	hostRe   = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`)
	healthRe = regexp.MustCompile(`^/[A-Za-z0-9._~/%?=&:@!$'()*+,;-]*$`)
	dirRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	reserved = map[string]bool{"droidd": true, "caddy": true, "panel": true, "admin": true, "api": true, "git": true, "www": true}
	runtimes = map[string]bool{"auto": true, "node": true, "python": true, "go": true, "static": true, "custom": true}
)

func ValidHost(h string) bool { return len(h) <= 253 && hostRe.MatchString(h) }

type RepoProvisioner interface {
	Init(ctx context.Context, app string) error
	Remove(ctx context.Context, app string) error
}

type Service struct {
	Store  *store.Store
	Runit  runit.ServiceManager
	Box    *crypt.Box
	Events *events.Log
	Cfg    config.Config
	Git    RepoProvisioner // opcional
	Wake   func()          // acorda o reconciliador
	// Deploying diz se há deploy em andamento (injetado pelo pacote deploy).
	Deploying func(appID int64) bool
	// PortFree testa se a porta está livre; substituível nos testes.
	PortFree func(port int) bool
	Prefix   string

	portMu  sync.Mutex
	touchMu sync.Mutex
	touched map[int64]time.Time
}

// Touch registra que o DroidPaaS mexeu no processo do app (start/stop/restart/deploy): o detector de crash loop
// não pode contar esses reinícios como quedas do app.
func (s *Service) Touch(id int64) {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	if s.touched == nil {
		s.touched = map[int64]time.Time{}
	}
	s.touched[id] = time.Now()
}

func (s *Service) LastTouch(id int64) time.Time {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	return s.touched[id]
}

func (s *Service) wake() {
	if s.Wake != nil {
		s.Wake()
	}
}

// PrefixDir é o $PREFIX usado nos scripts gerados.
func (s *Service) PrefixDir() string { return s.prefix() }

func (s *Service) prefix() string {
	if s.Prefix != "" {
		return s.Prefix
	}
	return runit.Prefix()
}

func (s *Service) ServiceName(app string) string { return s.Cfg.Runit.Prefix + "app-" + app }
func (s *Service) AppDir(app string) string      { return s.Cfg.Dir("apps", app) }
func (s *Service) LogDir(app string) string      { return s.Cfg.Dir("logs", "apps", app) }

// Spec monta o serviço runit do app (TSD §8.2.2). Nasce parado quando down=true.
func (s *Service) Spec(a store.App, down bool) runit.ServiceSpec {
	return runit.ServiceSpec{
		Name:      s.ServiceName(a.Name),
		Run:       runit.AppRun(s.prefix(), s.AppDir(a.Name), a.Nice),
		LogRun:    runit.LogRun(s.prefix(), s.LogDir(a.Name)),
		LogDir:    s.LogDir(a.Name),
		LogConfig: runit.LogConfig(1_000_000, 5),
		Down:      down,
	}
}

type CreateInput struct {
	Name, Runtime, Branch string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (a store.App, err error) {
	if in.Runtime == "" {
		in.Runtime = "auto"
	}
	if in.Branch == "" {
		in.Branch = "main"
	}
	switch {
	case !nameRe.MatchString(in.Name):
		return a, invalid("name", "use %s", nameRe.String())
	case reserved[in.Name]:
		return a, invalid("name", "%q é um nome reservado", in.Name)
	case !runtimes[in.Runtime]:
		return a, invalid("runtime", "use auto, node, python, go, static ou custom")
	case !branchRe.MatchString(in.Branch) || strings.Contains(in.Branch, ".."):
		return a, invalid("branch", "nome de branch inválido")
	}
	if _, err := s.Store.AppByName(ctx, in.Name); err == nil {
		return a, ErrConflict
	}

	a = store.App{Name: in.Name, Runtime: in.Runtime, Branch: in.Branch, HealthTimeoutS: int(s.Cfg.Deploy.HealthTimeout.D().Seconds()),
		Nice: s.Cfg.Build.Nice, DesiredState: "running"}
	port, err := s.allocPort(ctx)
	if err != nil {
		return a, err
	}
	a.Port = &port
	if err := s.Store.InsertApp(ctx, &a); err != nil {
		if errors.Is(err, store.ErrExists) {
			return a, ErrConflict
		}
		return a, err
	}
	// falha daqui em diante desfaz tudo o que foi criado
	defer func() {
		if err != nil {
			s.Runit.Remove(ctx, s.ServiceName(a.Name))
			if s.Git != nil {
				s.Git.Remove(ctx, a.Name)
			}
			os.RemoveAll(s.AppDir(a.Name))
			os.RemoveAll(s.LogDir(a.Name))
			s.Store.DeleteApp(ctx, a.ID)
		}
	}()
	for _, d := range []string{"env", "shared", "releases"} {
		if err = os.MkdirAll(filepath.Join(s.AppDir(a.Name), d), 0o700); err != nil {
			return a, err
		}
	}
	if s.Git != nil {
		if err = s.Git.Init(ctx, a.Name); err != nil {
			return a, err
		}
	}
	if err = s.Runit.Ensure(ctx, s.Spec(a, true)); err != nil {
		return a, err
	}
	if err = s.SyncAutoDomains(ctx); err != nil {
		return a, err
	}
	s.Events.Info(ctx, "app.created", &a.ID, "app criado: "+a.Name, map[string]any{"runtime": a.Runtime, "port": port})
	s.wake()
	return a, nil
}

// allocPort escolhe a menor porta livre da faixa (§8.9) e confirma com um listen de teste.
func (s *Service) allocPort(ctx context.Context) (int, error) {
	s.portMu.Lock()
	defer s.portMu.Unlock()
	used, err := s.Store.UsedPorts(ctx)
	if err != nil {
		return 0, err
	}
	free := s.PortFree
	if free == nil {
		free = func(p int) bool {
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
			if err != nil {
				return false
			}
			l.Close()
			return true
		}
	}
	for p := s.Cfg.Ports.RangeStart; p <= s.Cfg.Ports.RangeEnd; p++ {
		if !used[p] && free(p) {
			return p, nil
		}
	}
	return 0, errors.New("sem portas livres na faixa configurada")
}

func (s *Service) Get(ctx context.Context, name string) (store.App, error) {
	return s.Store.AppByName(ctx, name)
}
func (s *Service) List(ctx context.Context) ([]store.App, error) { return s.Store.ListApps(ctx) }

// State calcula o estado observado (§7.2); não é persistido, exceto `crashed`.
func (s *Service) State(ctx context.Context, a store.App) string {
	if a.CrashedAt != nil {
		return "crashed"
	}
	if s.Deploying != nil && s.Deploying(a.ID) {
		return "deploying"
	}
	// site estático não tem processo: está "no ar" quando a release está ativa e o app deve rodar
	if a.CurrentReleaseID != nil {
		if rel, err := s.Store.Release(ctx, *a.CurrentReleaseID); err == nil && rel.Runtime == "static" {
			if a.DesiredState == "running" {
				return "running"
			}
			return "stopped"
		}
	}
	st, err := s.Runit.Status(ctx, s.ServiceName(a.Name))
	if err != nil {
		if errors.Is(err, runit.ErrNotReady) {
			return "starting"
		}
		if errors.Is(err, runit.ErrNoService) && a.CurrentReleaseID != nil {
			return "error"
		}
		return "unknown"
	}
	if st.State == "down" {
		return "stopped"
	}
	if st.Uptime < 3*time.Second {
		return "starting"
	}
	return "running"
}

// Patch: ponteiros nil = não alterar; string vazia em campo opcional = limpar.
type Patch struct {
	Runtime        *string `json:"runtime"`
	Branch         *string `json:"branch"`
	BuildCmd       *string `json:"build_cmd"`
	StartCmd       *string `json:"start_cmd"`
	HealthPath     *string `json:"health_path"`
	HealthTimeoutS *int    `json:"health_timeout_s"`
	StaticDir      *string `json:"static_dir"`
	Nice           *int    `json:"nice"`
}

func optStr(v string, field string, max int) (*string, error) {
	if v == "" {
		return nil, nil
	}
	if len(v) > max || strings.ContainsAny(v, "\x00\r\n") {
		return nil, invalid(field, "inválido (até %d caracteres, sem quebra de linha)", max)
	}
	return &v, nil
}

func (s *Service) Update(ctx context.Context, name string, p Patch) (store.App, error) {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return a, err
	}
	if p.Runtime != nil {
		if !runtimes[*p.Runtime] {
			return a, invalid("runtime", "use auto, node, python, go, static ou custom")
		}
		a.Runtime = *p.Runtime
	}
	if p.Branch != nil {
		if !branchRe.MatchString(*p.Branch) || strings.Contains(*p.Branch, "..") {
			return a, invalid("branch", "nome de branch inválido")
		}
		a.Branch = *p.Branch
	}
	if p.BuildCmd != nil {
		if a.BuildCmd, err = optStr(*p.BuildCmd, "build_cmd", 500); err != nil {
			return a, err
		}
	}
	if p.StartCmd != nil {
		if a.StartCmd, err = optStr(*p.StartCmd, "start_cmd", 500); err != nil {
			return a, err
		}
	}
	if p.HealthPath != nil {
		if *p.HealthPath != "" && (!healthRe.MatchString(*p.HealthPath) || len(*p.HealthPath) > 200) {
			return a, invalid("health_path", "deve começar com / e ter só caracteres de URL")
		}
		a.HealthPath, _ = optStr(*p.HealthPath, "health_path", 200)
	}
	if p.StaticDir != nil {
		d := strings.TrimRight(*p.StaticDir, "/")
		if strings.HasPrefix(d, "/") || !dirRe.MatchString(d) || strings.Contains(d, "..") || len(d) > 100 {
			return a, invalid("static_dir", "caminho relativo inválido")
		}
		a.StaticDir, _ = optStr(d, "static_dir", 100)
	}
	if p.HealthTimeoutS != nil {
		if *p.HealthTimeoutS < 1 || *p.HealthTimeoutS > 600 {
			return a, invalid("health_timeout_s", "entre 1 e 600")
		}
		a.HealthTimeoutS = *p.HealthTimeoutS
	}
	nice := a.Nice
	if p.Nice != nil {
		if *p.Nice < 0 || *p.Nice > 19 {
			return a, invalid("nice", "entre 0 e 19")
		}
		a.Nice = *p.Nice
	}
	if err := s.Store.UpdateApp(ctx, a); err != nil {
		return a, err
	}
	if a.Nice != nice { // o nice vive no script run
		if err := s.Runit.Ensure(ctx, s.Spec(a, false)); err != nil {
			return a, err
		}
	}
	s.Events.Info(ctx, "app.updated", &a.ID, "app editado: "+a.Name, nil)
	s.wake()
	return a, nil
}

func (s *Service) Delete(ctx context.Context, name, confirm string) error {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return err
	}
	if confirm != name {
		return invalid("confirm", "repita o nome do app para confirmar")
	}
	if err := s.Runit.Remove(ctx, s.ServiceName(name)); err != nil {
		return err
	}
	if s.Git != nil {
		s.Git.Remove(ctx, name)
	}
	if err := s.Store.DeleteApp(ctx, a.ID); err != nil {
		return err
	}
	os.RemoveAll(s.AppDir(name))
	os.RemoveAll(s.LogDir(name))
	s.Events.Info(ctx, "app.deleted", nil, "app removido: "+name, nil)
	s.wake()
	return nil
}

// Start liga o app e limpa a marca de crash loop. Sem release não há o que rodar.
func (s *Service) Start(ctx context.Context, name string) (store.App, error) {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return a, err
	}
	if a.CurrentReleaseID == nil {
		return a, ErrNoRelease
	}
	if err := s.Store.SetDesiredState(ctx, a.ID, "running"); err != nil {
		return a, err
	}
	s.Store.SetCrashed(ctx, a.ID, nil)
	if err := s.Runit.Ensure(ctx, s.Spec(a, false)); err != nil {
		return a, err
	}
	if err := s.Runit.Up(ctx, s.ServiceName(name)); err != nil {
		return a, err
	}
	s.Touch(a.ID)
	s.Events.Info(ctx, "app.started", &a.ID, "app iniciado: "+name, nil)
	s.wake()
	return s.Store.AppByName(ctx, name)
}

func (s *Service) Stop(ctx context.Context, name string) (store.App, error) {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return a, err
	}
	if err := s.Store.SetDesiredState(ctx, a.ID, "stopped"); err != nil {
		return a, err
	}
	if err := s.Runit.Down(ctx, s.ServiceName(name)); err != nil && !errors.Is(err, runit.ErrNoService) {
		return a, err
	}
	s.Touch(a.ID)
	s.Events.Info(ctx, "app.stopped", &a.ID, "app parado: "+name, nil)
	s.wake()
	return s.Store.AppByName(ctx, name)
}

func (s *Service) Restart(ctx context.Context, name string) (store.App, error) {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return a, err
	}
	if a.CurrentReleaseID == nil {
		return a, ErrNoRelease
	}
	s.Store.SetDesiredState(ctx, a.ID, "running")
	s.Store.SetCrashed(ctx, a.ID, nil)
	if err := s.Runit.Ensure(ctx, s.Spec(a, false)); err != nil {
		return a, err
	}
	if err := s.Runit.Restart(ctx, s.ServiceName(name), 10*time.Second); err != nil {
		return a, err
	}
	s.Touch(a.ID)
	s.Events.Info(ctx, "app.restarted", &a.ID, "app reiniciado: "+name, nil)
	s.wake()
	return s.Store.AppByName(ctx, name)
}

// ---- domínios ----

func (s *Service) BaseDomain(ctx context.Context) string {
	if v, ok, _ := s.Store.Setting(ctx, "base_domain"); ok {
		return v
	}
	return s.Cfg.Proxy.BaseDomain
}

// SyncAutoDomains garante <app>.<base_domain> para cada app e remove domínios automáticos obsoletos.
func (s *Service) SyncAutoDomains(ctx context.Context) error {
	base := s.BaseDomain(ctx)
	apps, err := s.Store.ListApps(ctx)
	if err != nil {
		return err
	}
	all, err := s.Store.Domains(ctx, 0)
	if err != nil {
		return err
	}
	for _, a := range apps {
		want := ""
		if base != "" {
			want = a.Name + "." + base
		}
		have := false
		for _, d := range all {
			if d.AppID != a.ID || !d.Auto {
				continue
			}
			if d.Hostname == want {
				have = true
			} else if err := s.Store.RemoveDomain(ctx, a.ID, d.Hostname); err != nil {
				return err
			}
		}
		if want != "" && !have {
			if err := s.Store.AddDomain(ctx, a.ID, want, true, true); err != nil && !errors.Is(err, store.ErrExists) {
				return err
			}
		}
	}
	return nil
}

func (s *Service) AddDomain(ctx context.Context, name, host string) error {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return err
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if !ValidHost(host) {
		return invalid("hostname", "hostname inválido")
	}
	if err := s.Store.AddDomain(ctx, a.ID, host, false, false); err != nil {
		if errors.Is(err, store.ErrExists) {
			return ErrConflict
		}
		return err
	}
	s.Events.Info(ctx, "domain.added", &a.ID, "domínio adicionado: "+host, nil)
	s.wake()
	return nil
}

func (s *Service) RemoveDomain(ctx context.Context, name, host string) error {
	a, err := s.Store.AppByName(ctx, name)
	if err != nil {
		return err
	}
	ds, err := s.Store.Domains(ctx, a.ID)
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.Hostname == host && d.Auto {
			return invalid("hostname", "o domínio automático não pode ser removido (desative o domínio base nas configurações)")
		}
	}
	if err := s.Store.RemoveDomain(ctx, a.ID, host); err != nil {
		return err
	}
	s.Events.Info(ctx, "domain.removed", &a.ID, "domínio removido: "+host, nil)
	s.wake()
	return nil
}
