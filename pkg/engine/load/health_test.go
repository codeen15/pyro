package load

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCPUSampler(t *testing.T) {
	c := newCPUSampler()
	time.Sleep(200 * time.Millisecond)
	idle, mem := c.sample()
	if idle > 0.5 || mem <= 0 {
		t.Errorf("idle process: cpu %.2f, mem %.1f MB", idle, mem)
	}
	// Keep every usable core busy.
	stop := time.Now().Add(300 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for x := 0; time.Now().Before(stop); x++ {
				_ = x * x
			}
		}()
	}
	wg.Wait()
	if busy, _ := c.sample(); busy < 0.4 {
		t.Errorf("busy cores read as %.2f", busy)
	}
}

func TestSaturationWarning(t *testing.T) {
	var s saturation
	for _, v := range []float64{0.95, 0.99, 0.5, 0.92, 0.93} {
		s.observe(v)
	}
	if s.warning("x", time.Second) != "" {
		t.Error("two ticks should not warn")
	}
	s.observe(0.97)
	if w := s.warning("This machine", time.Second); !strings.Contains(w, "for 3s") {
		t.Errorf("warning %q", w)
	}
}
