// Package runit abstrai o supervisor de serviços (TSD §8.2). `Manager` fala com o runit real;
// `Fake` serve aos testes no PC (RNF-13).
package runit

import (
	"context"
	"errors"
	"time"
)

type ServiceSpec struct {
	Name      string // diretório do serviço, ex.: dp-app-minha-api
	Run       string // conteúdo do script run
	LogRun    string // conteúdo do log/run; vazio = sem serviço de log
	LogDir    string // diretório do svlogd (criado com o arquivo config)
	LogConfig string // conteúdo do config do svlogd
	Down      bool   // criar parado (arquivo down)
}

type ServiceStatus struct {
	State   string // run | down | finish | unknown
	PID     int
	Uptime  time.Duration
	WantsUp bool
}

var ErrNoService = errors.New("serviço inexistente")

// ErrNotReady: o diretório existe mas o runsv ainda não o assumiu (acontece logo depois de criar um serviço).
var ErrNotReady = errors.New("serviço ainda não supervisionado")

type ServiceManager interface {
	// Ensure cria ou atualiza o diretório do serviço de forma idempotente. Não altera o estado up/down de um serviço existente.
	Ensure(ctx context.Context, spec ServiceSpec) error
	Remove(ctx context.Context, name string) error
	Up(ctx context.Context, name string) error
	Down(ctx context.Context, name string) error
	Restart(ctx context.Context, name string, wait time.Duration) error
	Status(ctx context.Context, name string) (ServiceStatus, error)
	// List devolve os serviços cujo nome começa com prefix (ignora diretórios temporários `.dp-tmp-*`).
	List(ctx context.Context, prefix string) ([]string, error)
}
