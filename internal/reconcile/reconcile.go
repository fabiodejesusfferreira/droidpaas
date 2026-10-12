// Package reconcile roda os passos que convergem o estado observado ao desejado (TSD §8.7, ADR-04).
package reconcile

import (
	"context"
	"log/slog"
	"time"
)

// Step é um passo idempotente; falhar não pode corromper o estado.
type Step struct {
	Name string
	Run  func(ctx context.Context) error
}

type Loop struct {
	Interval time.Duration
	Steps    []Step
	Log      *slog.Logger
	wake     chan struct{}
}

func New(interval time.Duration, log *slog.Logger, steps ...Step) *Loop {
	return &Loop{Interval: interval, Steps: steps, Log: log, wake: make(chan struct{}, 1)}
}

// Wake pede um ciclo imediato (sem acumular pedidos).
func (l *Loop) Wake() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// Once executa um ciclo; um passo que falha não impede os seguintes.
func (l *Loop) Once(ctx context.Context) {
	for _, s := range l.Steps {
		if ctx.Err() != nil {
			return
		}
		if err := s.Run(ctx); err != nil && l.Log != nil {
			l.Log.Warn("passo do reconciliador falhou", "passo", s.Name, "err", err)
		}
	}
}

// Run bloqueia até ctx terminar: ciclo imediato, depois a cada Interval ou quando acordado.
func (l *Loop) Run(ctx context.Context) {
	t := time.NewTicker(l.Interval)
	defer t.Stop()
	l.Once(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-l.wake:
		}
		l.Once(ctx)
	}
}
