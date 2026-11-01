package load

import (
	"context"
	"errors"
	"sync"
	"time"
)

// pacer spaces request starts evenly so that, across every VU, no more than
// rate requests begin per second. Waiting happens before a request is sent,
// so it isn't counted in http_req_duration.
type pacer struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newPacer(rate float64) *pacer {
	if rate <= 0 {
		return nil
	}
	return &pacer{interval: time.Duration(float64(time.Second) / rate)}
}

// errStopping means a VU gave up its slot because its scenario is ending:
// a request that hasn't started by then isn't sent.
var errStopping = errors.New("stopping")

// wait blocks until this caller's slot (nil pacer: no limit). stopping is
// checked while waiting (every 100 ms), so a VU whose scenario ends doesn't
// send a queued request after the duration.
func (p *pacer) wait(ctx context.Context, stopping func() bool) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	slot := p.next
	p.next = p.next.Add(p.interval)
	p.mu.Unlock()
	for {
		d := time.Until(slot)
		if d <= 0 {
			return nil
		}
		if stopping() {
			return errStopping
		}
		timer := time.NewTimer(min(d, 100*time.Millisecond))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// effectiveRPS is the lowest positive cap of the plan, the run's options
// and the workspace's (0 = no cap).
func effectiveRPS(caps ...float64) float64 {
	out := 0.0
	for _, c := range caps {
		if c > 0 && (out == 0 || c < out) {
			out = c
		}
	}
	return out
}
