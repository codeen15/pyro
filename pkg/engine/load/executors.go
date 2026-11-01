package load

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codeen15/pyro/pkg/engine/metrics"
)

// feeder hands out data rows to VUs.
type feeder struct {
	rows  []map[string]string
	mode  string
	stop  bool
	index atomic.Int64
}

func newFeeder(rows []map[string]string, d *Data) *feeder {
	return &feeder{rows: rows, mode: d.Mode, stop: d.OnEnd == "stop"}
}

func (f *feeder) next(v *vu) (map[string]string, error) {
	n := int64(len(f.rows))
	switch f.mode {
	case "per-vu":
		return f.rows[int64(v.id-1)%n], nil
	case "random":
		return f.rows[v.rnd.Int64N(n)], nil
	}
	i := f.index.Add(1) - 1
	if i >= n && f.stop {
		return nil, errDataExhausted
	}
	return f.rows[i%n], nil
}

func runScenario(t *Test, name string, sc *Scenario, feed *feeder) {
	switch sc.Executor {
	case ConstantVUs:
		runVUs(t, name, sc, feed, func(elapsed time.Duration) (int, bool) {
			return sc.VUs, elapsed < sc.Duration.D()
		})
	case RampingVUs:
		total := stagesDuration(sc.Stages)
		runVUs(t, name, sc, feed, func(elapsed time.Duration) (int, bool) {
			return int(math.Round(stageValue(float64(sc.StartVUs), sc.Stages, elapsed))), elapsed < total
		})
	case PerVUIterations:
		runPerVU(t, name, sc, feed)
	case ConstantArrivalRate:
		runArrival(t, name, sc, feed, func(elapsed time.Duration) float64 { return sc.Rate }, sc.Duration.D())
	case RampingArrivalRate:
		runArrival(t, name, sc, feed, func(elapsed time.Duration) float64 {
			return stageValue(sc.StartRate, sc.Stages, elapsed)
		}, stagesDuration(sc.Stages))
	}
}

func stagesDuration(stages []Stage) time.Duration {
	var d time.Duration
	for _, s := range stages {
		d += s.Duration.D()
	}
	return d
}

// stageValue linearly interpolates a target through stages.
func stageValue(start float64, stages []Stage, elapsed time.Duration) float64 {
	from := start
	for _, s := range stages {
		d := s.Duration.D()
		if elapsed < d {
			return from + (s.Target-from)*float64(elapsed)/float64(d)
		}
		elapsed -= d
		from = s.Target
	}
	return from
}

// vuSlot is a running VU goroutine that can be asked to stop after its
// current iteration (soft) or cancelled (hard).
type vuSlot struct {
	v       *vu
	stop    atomic.Bool
	cancel  context.CancelFunc
	done    chan struct{}
	stopped time.Time
}

// launchVU starts a VU goroutine that iterates until asked to stop, the
// test stops, its data runs out, or it has done `iterations` (0 = no limit).
func launchVU(t *Test, v *vu, iterations int) *vuSlot {
	ctx, cancel := context.WithCancel(t.hard)
	s := &vuSlot{v: v, cancel: cancel, done: make(chan struct{})}
	v.stop = &s.stop
	t.active.Add(1)
	go func() {
		defer close(s.done)
		defer t.active.Add(-1)
		for n := 0; iterations == 0 || n < iterations; n++ {
			if s.stop.Load() || t.soft.Err() != nil {
				return
			}
			if err := v.iterate(ctx); err != nil && (errors.Is(err, errDataExhausted) || ctx.Err() != nil) {
				return
			}
		}
	}()
	return s
}

// gracefulEnd asks every slot to stop after its current iteration and
// cancels whatever is still running after gracefulStop.
func gracefulEnd(t *Test, slots []*vuSlot, grace time.Duration) {
	for _, s := range slots {
		s.stop.Store(true)
	}
	deadline := time.After(grace)
	for _, s := range slots {
		select {
		case <-s.done:
		case <-deadline:
			for _, s2 := range slots {
				s2.cancel()
			}
			<-s.done
		case <-t.hard.Done():
			<-s.done
		}
	}
	for _, s := range slots {
		s.cancel()
	}
}

// runPerVU: a fixed number of VUs each run a fixed number of iterations,
// bounded by maxDuration.
func runPerVU(t *Test, name string, sc *Scenario, feed *feeder) {
	slots := make([]*vuSlot, sc.VUs)
	all := make(chan struct{})
	for i := range slots {
		slots[i] = launchVU(t, t.newVU(name, sc, feed), sc.Iterations)
	}
	go func() {
		for _, s := range slots {
			<-s.done
		}
		close(all)
	}()
	select {
	case <-all:
	case <-time.After(sc.MaxDuration.D()):
	case <-t.soft.Done():
	}
	gracefulEnd(t, slots, sc.GracefulStop.D())
}

// runVUs drives constant-vus and ramping-vus. target reports the wanted VU
// count and whether the scenario is still running.
func runVUs(t *Test, name string, sc *Scenario, feed *feeder, target func(time.Duration) (int, bool)) {
	start := time.Now()
	var (
		slots []*vuSlot
		idle  []*vu // VUs from stopped slots, reused when scaling back up
	)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		elapsed := time.Since(start)
		want, running := target(elapsed)
		if !running || t.soft.Err() != nil {
			break
		}
		// Prune finished slots; their VUs can be reused.
		live := slots[:0]
		for _, s := range slots {
			select {
			case <-s.done:
				idle = append(idle, s.v)
			default:
				live = append(live, s)
			}
		}
		slots = live
		active := 0
		for _, s := range slots {
			if !s.stop.Load() {
				active++
			}
		}
		for ; active < want; active++ {
			var v *vu
			if n := len(idle); n > 0 {
				v, idle = idle[n-1], idle[:n-1]
			} else {
				v = t.newVU(name, sc, feed)
			}
			slots = append(slots, launchVU(t, v, 0))
		}
		// Ramp down: newest first, after their current iteration.
		for i := len(slots) - 1; i >= 0 && active > want; i-- {
			if !slots[i].stop.Load() {
				slots[i].stop.Store(true)
				slots[i].stopped = time.Now()
				active--
			}
		}
		// Ramp-down grace exceeded: cancel the stragglers.
		for _, s := range slots {
			if s.stop.Load() && !s.stopped.IsZero() && time.Since(s.stopped) > sc.GracefulRampDown.D() {
				s.cancel()
			}
		}
		select {
		case <-ticker.C:
		case <-t.soft.Done():
		}
	}

	// Scenario over: let in-flight iterations finish within gracefulStop.
	gracefulEnd(t, slots, sc.GracefulStop.D())
}

// runArrival drives open-model executors: iterations start on schedule
// whatever the latency. When every allocated VU is busy and maxVUs is
// reached, the iteration is dropped and counted.
func runArrival(t *Test, name string, sc *Scenario, feed *feeder, rate func(time.Duration) float64, duration time.Duration) {
	unit := sc.TimeUnit.D()
	pool := make(chan *vu, sc.MaxVUs)
	allocated := 0
	for ; allocated < sc.PreAllocatedVUs; allocated++ {
		pool <- t.newVU(name, sc, feed)
	}
	var (
		wg        sync.WaitGroup
		exhausted atomic.Bool
	)
	ctx, cancel := context.WithCancel(t.hard)
	defer cancel()
	dropTags := metrics.Tags{Scenario: name}

	start := time.Now()
	started := 0.0 // iterations started so far
	due := 0.0     // iterations that should have started by now
	last := start
	for t.soft.Err() == nil && !exhausted.Load() {
		now := time.Now()
		elapsed := now.Sub(start)
		if elapsed >= duration {
			break
		}
		// Integrate the rate over the step (trapezoid), in iterations.
		r0, r1 := rate(last.Sub(start)), rate(elapsed)
		due += (r0 + r1) / 2 * now.Sub(last).Seconds() / unit.Seconds()
		last = now
		for started+1 <= due {
			started++
			var v *vu
			select {
			case v = <-pool:
			default:
				if allocated < sc.MaxVUs {
					v = t.newVU(name, sc, feed)
					allocated++
				}
			}
			if v == nil {
				t.agg.Record(metrics.Sample{Metric: "dropped_iterations", Tags: dropTags, Value: 1})
				continue
			}
			wg.Add(1)
			t.active.Add(1)
			go func(v *vu) {
				defer wg.Done()
				defer t.active.Add(-1)
				if err := v.iterate(ctx); errors.Is(err, errDataExhausted) {
					exhausted.Store(true)
				}
				pool <- v
			}(v)
		}
		// Sleep until roughly the next iteration is due (1ms–50ms).
		wait := 50 * time.Millisecond
		if cur := rate(elapsed); cur > 0 {
			next := time.Duration(float64(unit) / cur)
			wait = min(max(next, time.Millisecond), wait)
		}
		select {
		case <-time.After(wait):
		case <-t.soft.Done():
		}
	}

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(sc.GracefulStop.D()):
		cancel()
		<-finished
	case <-t.hard.Done():
		<-finished
	}
}
