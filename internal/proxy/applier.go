package proxy

import (
	"context"
	"crypto/sha256"
	"time"
)

// Applier só chama o Caddy quando a config muda, e recua (backoff) quando ele falha (TSD §8.5.3).
type Applier struct {
	Client ProxyClient
	Now    func() time.Time
	// OnFail é chamado a cada falha de aplicação (o chamador registra o evento proxy.apply_failed).
	OnFail func(err error, retryIn time.Duration)

	last     [32]byte
	have     bool
	failures int
	nextTry  time.Time
}

const maxBackoff = 60 * time.Second

// Apply devolve applied=true quando enviou uma config nova ao Caddy com sucesso.
func (a *Applier) Apply(ctx context.Context, cfg []byte) (applied bool, err error) {
	now := a.Now()
	sum := sha256.Sum256(cfg)
	if a.have && sum == a.last {
		return false, nil
	}
	if now.Before(a.nextTry) {
		return false, nil // dentro da janela de backoff
	}
	if err := a.Client.Load(ctx, cfg); err != nil {
		a.failures++
		d := time.Duration(1<<min(a.failures, 6)) * time.Second // 2 s, 4 s, … 60 s
		d = min(d, maxBackoff)
		a.nextTry = now.Add(d)
		if a.OnFail != nil {
			a.OnFail(err, d)
		}
		return false, err // a config anterior continua ativa no Caddy
	}
	a.last, a.have, a.failures, a.nextTry = sum, true, 0, time.Time{}
	return true, nil
}

// Invalidate força a reaplicação na próxima chamada (ex.: o dp-caddy reiniciou e voltou ao bootstrap, PoC V-04).
func (a *Applier) Invalidate() { a.have = false; a.nextTry = time.Time{} }
