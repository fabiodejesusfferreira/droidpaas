package system

import (
	"sync"
	"time"
)

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// FakeClock avança só quando o teste manda.
type FakeClock struct {
	mu sync.Mutex
	T  time.Time
}

func (c *FakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.T }
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.T = c.T.Add(d)
	c.mu.Unlock()
}
