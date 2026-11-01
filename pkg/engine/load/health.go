package load

import (
	"fmt"
	"runtime"
	"runtime/metrics"
	"time"
)

// cpuSampler measures this process's CPU use between calls: process CPU
// time over the wall time available to GOMAXPROCS threads, so 1.0 means
// every usable core is busy. Memory comes from the Go runtime.
type cpuSampler struct {
	mem     []metrics.Sample
	cpu     time.Duration
	at      time.Time
	working bool
}

func newCPUSampler() *cpuSampler {
	c := &cpuSampler{mem: []metrics.Sample{{Name: "/memory/classes/total:bytes"}}, at: time.Now()}
	c.cpu, c.working = processCPU()
	return c
}

// sample returns CPU use since the previous call (0..1, or -1 where the OS
// doesn't report it) and memory in MB.
func (c *cpuSampler) sample() (cpu, memMB float64) {
	metrics.Read(c.mem)
	if c.mem[0].Value.Kind() == metrics.KindUint64 {
		memMB = float64(c.mem[0].Value.Uint64()) / (1 << 20)
	}
	now := time.Now()
	used, ok := processCPU()
	if !ok || !c.working {
		return -1, memMB
	}
	wall := now.Sub(c.at) * time.Duration(runtime.GOMAXPROCS(0))
	if wall > 0 {
		cpu = min(max(float64(used-c.cpu)/float64(wall), 0), 1)
	}
	c.cpu, c.at = used, now
	return cpu, memMB
}

// saturation tracks how long a load generator stayed CPU-bound.
type saturation struct {
	run, longest int // consecutive ticks at or above the limit
}

const saturatedCPU = 0.9

func (s *saturation) observe(cpu float64) {
	if cpu >= saturatedCPU {
		s.run++
		s.longest = max(s.longest, s.run)
		return
	}
	s.run = 0
}

// warning explains a saturated generator, or "" (3 ticks or more count).
func (s *saturation) warning(who string, tick time.Duration) string {
	if s.longest < 3 {
		return ""
	}
	return fmt.Sprintf("%s's CPU was at %.0f%% or more for %s: latencies may include time Pyro spent waiting for CPU. Use fewer VUs per machine, lighter scripts, or more agents.",
		who, saturatedCPU*100, (time.Duration(s.longest) * tick).Round(time.Second))
}
