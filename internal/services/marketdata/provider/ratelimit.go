package provider

import (
	"context"
	"time"
)

// Limiter is a simple token-bucket rate limiter backed by time.Ticker.
// It is safe for concurrent use: multiple goroutines may call Wait
// simultaneously and each will receive tokens at the configured rate.
type Limiter struct {
	ch   chan struct{}
	stop chan struct{}
}

// NewLimiter creates a Limiter that allows at most reqPerMin requests per minute.
// The first token is available immediately.
func NewLimiter(reqPerMin int) *Limiter {
	if reqPerMin <= 0 {
		reqPerMin = 1
	}
	interval := time.Minute / time.Duration(reqPerMin)
	l := &Limiter{
		ch:   make(chan struct{}, 1),
		stop: make(chan struct{}),
	}
	// Pre-fill one token so the first call does not block.
	l.ch <- struct{}{}

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				select {
				case l.ch <- struct{}{}:
				default: // bucket full; drop token
				}
			case <-l.stop:
				return
			}
		}
	}()
	return l
}

// Wait blocks until a token is available or ctx is cancelled.
func (l *Limiter) Wait(ctx context.Context) error {
	select {
	case <-l.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop shuts down the background ticker goroutine.
func (l *Limiter) Stop() { close(l.stop) }
