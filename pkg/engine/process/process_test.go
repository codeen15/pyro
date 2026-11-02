package process

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/codeen15/pyro/pkg/engine/model"
)

func TestEffectiveTimer(t *testing.T) {
	coll := &model.Collection{Think: &model.Timer{Type: model.TimerConstant, Delay: "1s"}}
	folder := &model.Collection{Think: &model.Timer{Type: model.TimerConstant, Delay: "2s"}}
	off := &model.Collection{Think: &model.Timer{Type: "none"}}
	req := &model.Request{}
	if got := EffectiveTimer(req, []*model.Collection{coll, folder}); got.Delay != "2s" {
		t.Fatalf("nearest folder should win, got %+v", got)
	}
	if got := EffectiveTimer(req, []*model.Collection{coll, off}); got != nil {
		t.Fatalf("none should turn the timer off, got %+v", got)
	}
	req.Think = &model.Timer{Type: model.TimerUniform, Delay: "10ms", Range: "5ms"}
	if got := EffectiveTimer(req, []*model.Collection{coll}); got != req.Think {
		t.Fatal("the request's own timer should win")
	}
	if EffectiveTimer(&model.Request{}, nil) != nil {
		t.Fatal("no timer anywhere")
	}
}

func TestSampleDistributions(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	mean := func(tm *model.Timer) (lo, hi, avg time.Duration) {
		lo = time.Hour
		var sum time.Duration
		const n = 4000
		for range n {
			d := Sample(tm, rnd)
			lo, hi, sum = min(lo, d), max(hi, d), sum+d
		}
		return lo, hi, sum / n
	}
	if lo, hi, _ := mean(&model.Timer{Type: model.TimerConstant, Delay: "250ms"}); lo != 250*time.Millisecond || hi != lo {
		t.Fatalf("constant: %v..%v", lo, hi)
	}
	lo, hi, avg := mean(&model.Timer{Type: model.TimerUniform, Delay: "100ms", Range: "100ms"})
	if lo < 100*time.Millisecond || hi > 200*time.Millisecond || avg < 140*time.Millisecond || avg > 160*time.Millisecond {
		t.Fatalf("uniform: %v..%v avg %v", lo, hi, avg)
	}
	lo, _, avg = mean(&model.Timer{Type: model.TimerGaussian, Delay: "300ms", Range: "50ms"})
	if lo < 0 || avg < 290*time.Millisecond || avg > 310*time.Millisecond {
		t.Fatalf("gaussian: lo %v avg %v", lo, avg)
	}
	_, _, avg = mean(&model.Timer{Type: model.TimerPoisson, Delay: "0", Range: "40ms"})
	if avg < 38*time.Millisecond || avg > 42*time.Millisecond {
		t.Fatalf("poisson: avg %v", avg)
	}
	if d := Sample(&model.Timer{Type: model.TimerConstant, Delay: "150"}, rnd); d != 150*time.Millisecond {
		t.Fatalf("bare numbers are ms, got %v", d)
	}
}

func TestValidate(t *testing.T) {
	for _, tm := range []*model.Timer{{Type: "sometimes"}, {Type: model.TimerConstant, Delay: "soon"}, {Type: model.TimerUniform, Range: "-1s"}} {
		if ValidateTimer(tm) == nil {
			t.Errorf("%+v should be invalid", tm)
		}
	}
	if err := ValidateTimer(&model.Timer{Type: model.TimerGaussian, Delay: "1s", Range: "200ms"}); err != nil {
		t.Fatal(err)
	}
}
