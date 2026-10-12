package runit

import (
	"context"
	"sync"
	"time"
)

// Fake simula o runit em memória para testes no PC.
type Fake struct {
	mu   sync.Mutex
	Svcs map[string]*FakeService
	Log  []string // chamadas na ordem: "ensure x", "up x", ...
}

type FakeService struct {
	Spec   ServiceSpec
	Up     bool
	PID    int
	Starts int
}

func NewFake() *Fake { return &Fake{Svcs: map[string]*FakeService{}} }

func (f *Fake) rec(s string) { f.Log = append(f.Log, s) }

func (f *Fake) Ensure(_ context.Context, spec ServiceSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("ensure " + spec.Name)
	if s, ok := f.Svcs[spec.Name]; ok {
		s.Spec = spec
		return nil
	}
	s := &FakeService{Spec: spec}
	f.Svcs[spec.Name] = s
	if !spec.Down { // como o runsv: serviço novo sem arquivo `down` sobe sozinho
		f.start(s)
	}
	return nil
}

func (f *Fake) get(name string) (*FakeService, error) {
	s, ok := f.Svcs[name]
	if !ok {
		return nil, ErrNoService
	}
	return s, nil
}

func (f *Fake) start(s *FakeService) { s.Up = true; s.Starts++; s.PID = 1000 + s.Starts }

func (f *Fake) Up(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("up " + name)
	s, err := f.get(name)
	if err != nil {
		return err
	}
	if !s.Up {
		f.start(s)
	}
	return nil
}

func (f *Fake) Down(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("down " + name)
	s, err := f.get(name)
	if err != nil {
		return err
	}
	s.Up = false
	return nil
}

func (f *Fake) Restart(_ context.Context, name string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("restart " + name)
	s, err := f.get(name)
	if err != nil {
		return err
	}
	f.start(s)
	return nil
}

func (f *Fake) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec("remove " + name)
	delete(f.Svcs, name)
	return nil
}

func (f *Fake) Status(_ context.Context, name string) (ServiceStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.get(name)
	if err != nil {
		return ServiceStatus{State: "unknown"}, err
	}
	if s.Up {
		return ServiceStatus{State: "run", PID: s.PID, Uptime: 30 * time.Second, WantsUp: true}, nil
	}
	return ServiceStatus{State: "down"}, nil
}

// Crash simula a morte do processo seguida do reinício do runit (PID novo, uptime curto).
func (f *Fake) Crash(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.Svcs[name]; ok && s.Up {
		f.start(s)
	}
}
