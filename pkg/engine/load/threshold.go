package load

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/metrics"
)

var (
	keyRe  = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:\{([^}]*)\})?\s*$`)
	condRe = regexp.MustCompile(`^\s*(avg|min|max|med|count|rate|value|p\(\s*([0-9.]+)\s*\))\s*(<=|>=|==|!=|<|>)\s*(-?[0-9.]+)\s*$`)
)

func parseThresholdKey(key string) (string, map[string]string, error) {
	m := keyRe.FindStringSubmatch(key)
	if m == nil {
		return "", nil, fmt.Errorf("expected metric or metric{tag:value}")
	}
	filter := map[string]string{}
	if m[2] != "" {
		for _, part := range strings.Split(m[2], ",") {
			k, v, ok := strings.Cut(part, ":")
			if !ok || strings.TrimSpace(k) == "" {
				return "", nil, fmt.Errorf("tag filter %q must be tag:value", part)
			}
			filter[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m[1], filter, nil
}

type condition struct {
	agg   string
	p     float64
	op    string
	value float64
}

func parseCondition(s string) (condition, error) {
	m := condRe.FindStringSubmatch(s)
	if m == nil {
		return condition{}, fmt.Errorf("invalid condition %q (examples: p(95)<500, rate<0.01, avg<=200, count>100)", s)
	}
	c := condition{agg: m[1], op: m[3]}
	if strings.HasPrefix(m[1], "p(") {
		c.agg = "p"
		c.p, _ = strconv.ParseFloat(m[2], 64)
		if c.p <= 0 || c.p > 100 {
			return condition{}, fmt.Errorf("percentile in %q must be between 0 and 100", s)
		}
	}
	c.value, _ = strconv.ParseFloat(m[4], 64)
	return c, nil
}

func (c condition) actual(st metrics.Stat) float64 {
	switch c.agg {
	case "avg":
		return st.Avg
	case "min":
		return st.Min
	case "max":
		return st.Max
	case "med":
		return st.Med
	case "count":
		if st.Type == "counter" {
			return st.Value
		}
		return float64(st.Count)
	case "p":
		return st.Percentile(c.p)
	}
	return st.Value // rate, value
}

func (c condition) ok(actual float64) bool {
	switch c.op {
	case "<":
		return actual < c.value
	case "<=":
		return actual <= c.value
	case ">":
		return actual > c.value
	case ">=":
		return actual >= c.value
	case "==":
		return actual == c.value
	}
	return actual != c.value
}

// ThresholdResult is the current state of one condition.
type ThresholdResult struct {
	Metric      string  `json:"metric"`
	Condition   string  `json:"condition"`
	Actual      float64 `json:"actual"`
	Passed      bool    `json:"passed"`
	NoData      bool    `json:"noData,omitempty"`
	AbortOnFail bool    `json:"abortOnFail,omitempty"`
	// Breached is set once the condition has failed at any evaluation, even
	// if it passes now. It is informational: the verdict uses Passed.
	Breached bool `json:"breached,omitempty"`
}

type threshold struct {
	key    string
	metric string
	filter map[string]string
	crit   Criterion
	cond   condition
	breach bool
}

type thresholds []*threshold

func compileThresholds(p *Plan) thresholds {
	var out thresholds
	keys := make([]string, 0, len(p.Thresholds))
	for k := range p.Thresholds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		metric, filter, _ := parseThresholdKey(k)
		for _, c := range p.Thresholds[k] {
			cond, _ := parseCondition(c.Threshold)
			out = append(out, &threshold{key: k, metric: metric, filter: filter, crit: c, cond: cond})
		}
	}
	return out
}

// evaluate checks every threshold against cumulative data. abort is true
// when an abortOnFail threshold failed after its delay.
func (ts thresholds) evaluate(agg *metrics.Aggregator, elapsed time.Duration) (results []ThresholdResult, abort bool) {
	for _, t := range ts {
		st := agg.Query(t.metric, t.filter)
		r := ThresholdResult{Metric: t.key, Condition: t.crit.Threshold, AbortOnFail: t.crit.AbortOnFail}
		if st.Count == 0 && metrics.TypeOf(t.metric) != metrics.Counter {
			// No data yet: neither pass nor fail, and never abort on it.
			// (A counter with no samples is simply zero, evaluated below.)
			r.NoData, r.Passed = true, true
		} else {
			r.Actual = t.cond.actual(st)
			r.Passed = t.cond.ok(r.Actual)
			if !r.Passed {
				t.breach = true
				if t.crit.AbortOnFail && elapsed >= t.crit.DelayAbortEval.D() {
					abort = true
				}
			}
		}
		r.Breached = t.breach
		results = append(results, r)
	}
	return results, abort
}
