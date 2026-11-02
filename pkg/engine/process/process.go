// Package process implements JMeter-style timers and the built-in
// processors that need no script runtime: setting variables and counters.
// The engine runs scripts, extractors, assertions and plugin processors
// itself; this package holds the parts the load engine and runner share.
package process

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

// EffectiveTimer returns the timer that applies to a request: its own, or
// the nearest folder's, or the collection's. A timer of type "none" stops
// the search and means no think time.
func EffectiveTimer(req *model.Request, chain []*model.Collection) *model.Timer {
	t := req.Think
	for i := len(chain) - 1; t == nil && i >= 0; i-- {
		if chain[i] != nil {
			t = chain[i].Think
		}
	}
	if t == nil || t.Type == "none" || t.Type == "" {
		return nil
	}
	return t
}

// ValidateTimer reports a problem with a timer's type or durations.
func ValidateTimer(t *model.Timer) error {
	if t == nil {
		return nil
	}
	switch t.Type {
	case "", "none":
		return nil
	case model.TimerConstant, model.TimerUniform, model.TimerGaussian, model.TimerPoisson:
	default:
		return fmt.Errorf("unknown timer type %q (constant, uniform, gaussian, poisson or none)", t.Type)
	}
	if _, err := duration(t.Delay); err != nil {
		return fmt.Errorf("timer delay: %v", err)
	}
	if _, err := duration(t.Range); err != nil {
		return fmt.Errorf("timer range: %v", err)
	}
	return nil
}

func duration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// A bare number is milliseconds, as in JMeter.
		if ms, err2 := strconv.ParseFloat(s, 64); err2 == nil {
			d = time.Duration(ms * float64(time.Millisecond))
		} else {
			return 0, err
		}
	}
	if d < 0 {
		return 0, fmt.Errorf("negative duration %s", s)
	}
	return d, nil
}

// Sample draws one think time from a timer. An invalid timer waits nothing
// (plans are validated before they run).
func Sample(t *model.Timer, rnd *rand.Rand) time.Duration {
	if t == nil {
		return 0
	}
	delay, err1 := duration(t.Delay)
	spread, err2 := duration(t.Range)
	if err1 != nil || err2 != nil {
		return 0
	}
	var extra float64
	switch t.Type {
	case model.TimerUniform:
		extra = rnd.Float64() * float64(spread)
	case model.TimerGaussian:
		extra = rnd.NormFloat64() * float64(spread)
	case model.TimerPoisson:
		extra = float64(poisson(rnd, float64(spread)/float64(time.Millisecond))) * float64(time.Millisecond)
	}
	return max(delay+time.Duration(extra), 0)
}

// poisson draws from a Poisson distribution with the given mean (in ms).
// Knuth's method for small means; a normal approximation for large ones.
func poisson(rnd *rand.Rand, mean float64) int {
	if mean <= 0 {
		return 0
	}
	if mean > 500 {
		return max(int(math.Round(mean+rnd.NormFloat64()*math.Sqrt(mean))), 0)
	}
	l, k, p := math.Exp(-mean), 0, 1.0
	for {
		p *= rnd.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

// SetVars applies a vars processor: each enabled key is set, in the run's
// runtime scope, to its value with variables and functions resolved (in
// order, so later values can use earlier ones).
func SetVars(kvs []model.KeyValue, v *vars.Set) []string {
	var missing []string
	for _, kv := range kvs {
		if kv.Disabled || strings.TrimSpace(kv.Key) == "" {
			continue
		}
		val, m := v.Resolver().Resolve(kv.Value)
		missing = append(missing, m...)
		v.Runtime[strings.TrimSpace(kv.Key)] = val
	}
	return missing
}

// Count advances a counter stored in the run's runtime variables and
// returns the new value. The raw number is kept in a hidden companion
// variable so a Format like "%05d" doesn't break counting.
func Count(c *model.Counter, v *vars.Set) (string, error) {
	if c == nil || strings.TrimSpace(c.Var) == "" {
		return "", fmt.Errorf("counter needs a variable name")
	}
	step := c.Step
	if step == 0 {
		step = 1
	}
	key := "__counter." + c.Var
	n := c.Start
	if raw, ok := v.Runtime[key]; ok {
		cur, err := strconv.Atoi(raw)
		if err != nil {
			cur = c.Start - step
		}
		n = cur + step
		if c.End != 0 && ((step > 0 && n > c.End) || (step < 0 && n < c.End)) {
			n = c.Start
		}
	}
	v.Runtime[key] = strconv.Itoa(n)
	out := strconv.Itoa(n)
	if c.Format != "" {
		out = fmt.Sprintf(c.Format, n)
		if strings.Contains(out, "%!") {
			return "", fmt.Errorf("counter format %q doesn't take an integer", c.Format)
		}
	}
	v.Runtime[c.Var] = out
	return out, nil
}

// ValidateProcessor reports a processor that can't run in a phase.
func ValidateProcessor(p model.Processor, pre bool) error {
	switch {
	case strings.HasPrefix(p.Type, "plugin:"):
		return nil
	case p.Type == model.ProcScript, p.Type == model.ProcVars:
		return nil
	case p.Type == model.ProcCounter:
		if !pre {
			return fmt.Errorf("a counter is a pre-processor")
		}
		if p.Counter == nil || strings.TrimSpace(p.Counter.Var) == "" {
			return fmt.Errorf("counter needs a variable name")
		}
		return nil
	case p.Type == model.ProcExtract, p.Type == model.ProcAssert:
		if pre {
			return fmt.Errorf("%s is a post-processor", p.Type)
		}
		if p.Type == model.ProcExtract && (p.Extract == nil || p.Extract.Var == "") {
			return fmt.Errorf("extractor needs a variable name")
		}
		if p.Type == model.ProcAssert && p.Assert == nil {
			return fmt.Errorf("assertion is empty")
		}
		return nil
	}
	return fmt.Errorf("unknown processor type %q", p.Type)
}

// Label names a processor in logs and errors.
func Label(p model.Processor, level string, pre bool, i int) string {
	if p.Name != "" {
		return p.Name
	}
	phase := "post-processor"
	if pre {
		phase = "pre-processor"
	}
	return fmt.Sprintf("%s %s %d (%s)", level, phase, i+1, p.Type)
}
