package reconcile

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnceContinuesAfterFailure(t *testing.T) {
	var a, b int
	l := New(time.Hour, nil,
		Step{"falha", func(context.Context) error { a++; return errors.New("x") }},
		Step{"ok", func(context.Context) error { b++; return nil }})
	l.Once(context.Background())
	if a != 1 || b != 1 {
		t.Fatalf("a=%d b=%d", a, b)
	}
}

func TestRunWakeAndStop(t *testing.T) {
	var n atomic.Int32
	l := New(time.Hour, nil, Step{"c", func(context.Context) error { n.Add(1); return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx); close(done) }()
	waitFor := func(want int32) {
		for i := 0; i < 200 && n.Load() < want; i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if n.Load() < want {
			t.Fatalf("ciclos = %d, esperado >= %d", n.Load(), want)
		}
	}
	waitFor(1) // ciclo imediato
	l.Wake()
	l.Wake() // pedidos repetidos não se acumulam
	waitFor(2)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run não terminou com o contexto")
	}
}
