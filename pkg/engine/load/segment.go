package load

import "math"

// Segment returns the share of p that worker i of n runs: VU counts are
// split as evenly as whole numbers allow (so they sum to the original),
// arrival rates are divided exactly. Scenarios that get no VUs in this
// segment are left out; the result may have no scenarios at all.
// Thresholds are dropped, since only the controller sees all the samples.
func (p *Plan) Segment(i, n int) *Plan {
	out := *p
	out.Thresholds = nil
	out.Scenarios = map[string]*Scenario{}
	if n <= 1 {
		for name, sc := range p.Scenarios {
			out.Scenarios[name] = sc
		}
		return &out
	}
	for name, src := range p.Scenarios {
		sc := *src
		switch sc.Executor {
		case ConstantVUs, PerVUIterations:
			sc.VUs = share(sc.VUs, i, n)
			if sc.VUs == 0 {
				continue
			}
		case RampingVUs:
			sc.StartVUs = share(sc.StartVUs, i, n)
			sc.Stages = make([]Stage, len(src.Stages))
			peak := sc.StartVUs
			for j, st := range src.Stages {
				st.Target = float64(share(int(math.Round(st.Target)), i, n))
				sc.Stages[j] = st
				peak = max(peak, int(st.Target))
			}
			if peak == 0 {
				continue
			}
		case ConstantArrivalRate, RampingArrivalRate:
			sc.Rate /= float64(n)
			sc.StartRate /= float64(n)
			sc.Stages = make([]Stage, len(src.Stages))
			for j, st := range src.Stages {
				st.Target /= float64(n)
				sc.Stages[j] = st
			}
			// Every segment needs VUs to run its share of iterations.
			sc.PreAllocatedVUs = max(1, share(sc.PreAllocatedVUs, i, n))
			sc.MaxVUs = max(sc.PreAllocatedVUs, share(sc.MaxVUs, i, n))
		}
		out.Scenarios[name] = &sc
	}
	return &out
}

// share is worker i's part of total split n ways; the parts sum to total.
func share(total, i, n int) int {
	return total*(i+1)/n - total*i/n
}

// firstVU is the VU id offset for worker i, so per-vu data rows and VU ids
// don't repeat across workers.
func firstVU(p *Plan, i, n int) int {
	off := 0
	for j := 0; j < i; j++ {
		off += PeakVUs(p.Segment(j, n))
	}
	return off
}
