// Package runit abstrai o supervisor de serviços (TSD §8.2). A implementação
// real e o fake chegam na Fase 2C.
package runit

import (
	"context"
	"time"
)

type ServiceSpec struct {
	Name string
	Run  string // conteúdo do script run
	Log  string // conteúdo do script log/run
}

type ServiceStatus struct {
	State   string // run | down | finish | unknown
	PID     int
	Uptime  time.Duration
	WantsUp bool
}

type ServiceManager interface {
	Ensure(ctx context.Context, spec ServiceSpec) error
	Remove(ctx context.Context, name string) error
	Up(ctx context.Context, name string) error
	Down(ctx context.Context, name string) error
	Restart(ctx context.Context, name string, wait time.Duration) error
	Status(ctx context.Context, name string) (ServiceStatus, error)
}
