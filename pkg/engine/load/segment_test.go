package load

import "testing"

func TestSegmentSplitsLoad(t *testing.T) {
	p := &Plan{Name: "x", Thresholds: map[string]Criteria{"http_req_failed": {{Threshold: "rate<0.01"}}}, Scenarios: map[string]*Scenario{
		"c": {Executor: ConstantVUs, VUs: 10},
		"r": {Executor: RampingVUs, StartVUs: 1, Stages: []Stage{{Target: 7}, {Target: 0}}},
		"a": {Executor: ConstantArrivalRate, Rate: 90, PreAllocatedVUs: 5, MaxVUs: 20},
		"p": {Executor: PerVUIterations, VUs: 2, Iterations: 3},
	}}
	const n = 3
	vus, start, stage0, rate, maxVUs, perVU := 0, 0, 0.0, 0.0, 0, 0
	for i := 0; i < n; i++ {
		s := p.Segment(i, n)
		if s.Thresholds != nil {
			t.Fatal("segments must not carry thresholds")
		}
		vus += s.Scenarios["c"].VUs
		start += s.Scenarios["r"].StartVUs
		stage0 += s.Scenarios["r"].Stages[0].Target
		rate += s.Scenarios["a"].Rate
		maxVUs += s.Scenarios["a"].MaxVUs
		if sc := s.Scenarios["p"]; sc != nil {
			perVU += sc.VUs
			if sc.Iterations != 3 {
				t.Errorf("iterations per VU changed: %d", sc.Iterations)
			}
		}
	}
	if vus != 10 || start != 1 || stage0 != 7 || rate != 90 || maxVUs != 20 || perVU != 2 {
		t.Errorf("totals: vus=%d start=%d stage0=%v rate=%v maxVUs=%d perVU=%d", vus, start, stage0, rate, maxVUs, perVU)
	}
	if p.Scenarios["c"].VUs != 10 || len(p.Thresholds) != 1 {
		t.Error("Segment modified the original plan")
	}
	// 2 VUs over 3 workers: one worker gets none and drops the scenario.
	if _, ok := p.Segment(0, n).Scenarios["p"]; ok {
		t.Error("worker 0 should have no per-vu-iterations VUs")
	}
	if got := firstVU(p, 2, n); got != PeakVUs(p.Segment(0, n))+PeakVUs(p.Segment(1, n)) {
		t.Errorf("firstVU = %d", got)
	}
}
