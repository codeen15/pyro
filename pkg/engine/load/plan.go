// Package load runs load tests: scenarios of virtual users (VUs) executing
// flows of workspace requests under an executor, with live metrics and
// thresholds. See docs/load-engine.md for the plan format.
package load

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"gopkg.in/yaml.v3"

	"github.com/pyrohq/pyro/pkg/engine/model"
)

// Plan is a parsed *.load.yaml file.
type Plan struct {
	Name        string               `yaml:"name" json:"name"`
	Description string               `yaml:"description,omitempty" json:"description,omitempty"`
	Environment string               `yaml:"environment,omitempty" json:"environment,omitempty"`
	Scenarios   map[string]*Scenario `yaml:"scenarios" json:"scenarios"`
	Thresholds  map[string]Criteria  `yaml:"thresholds,omitempty" json:"thresholds,omitempty"`
}

// Executor names.
const (
	ConstantVUs         = "constant-vus"
	RampingVUs          = "ramping-vus"
	PerVUIterations     = "per-vu-iterations"
	ConstantArrivalRate = "constant-arrival-rate"
	RampingArrivalRate  = "ramping-arrival-rate"
)

type Scenario struct {
	Executor  string   `yaml:"executor" json:"executor"`
	StartTime Duration `yaml:"startTime,omitempty" json:"startTime,omitempty"`

	VUs      int      `yaml:"vus,omitempty" json:"vus,omitempty"`
	Duration Duration `yaml:"duration,omitempty" json:"duration,omitempty"`

	StartVUs         int      `yaml:"startVUs,omitempty" json:"startVUs,omitempty"`
	Stages           []Stage  `yaml:"stages,omitempty" json:"stages,omitempty"`
	GracefulRampDown Duration `yaml:"gracefulRampDown,omitempty" json:"gracefulRampDown,omitempty"`
	GracefulStop     Duration `yaml:"gracefulStop,omitempty" json:"gracefulStop,omitempty"`

	Iterations  int      `yaml:"iterations,omitempty" json:"iterations,omitempty"`
	MaxDuration Duration `yaml:"maxDuration,omitempty" json:"maxDuration,omitempty"`

	Rate            float64  `yaml:"rate,omitempty" json:"rate,omitempty"`
	StartRate       float64  `yaml:"startRate,omitempty" json:"startRate,omitempty"`
	TimeUnit        Duration `yaml:"timeUnit,omitempty" json:"timeUnit,omitempty"`
	PreAllocatedVUs int      `yaml:"preAllocatedVUs,omitempty" json:"preAllocatedVUs,omitempty"`
	MaxVUs          int      `yaml:"maxVUs,omitempty" json:"maxVUs,omitempty"`

	Flow []Step `yaml:"flow" json:"flow"`
	Data *Data  `yaml:"data,omitempty" json:"data,omitempty"`
}

// Stage targets VUs (ramping-vus) or iterations per time unit (ramping-arrival-rate).
type Stage struct {
	Duration Duration `yaml:"duration" json:"duration"`
	Target   float64  `yaml:"target" json:"target"`
}

type Data struct {
	File  string `yaml:"file" json:"file"`
	Mode  string `yaml:"mode,omitempty" json:"mode,omitempty"`   // shared | per-vu | random
	OnEnd string `yaml:"onEnd,omitempty" json:"onEnd,omitempty"` // recycle | stop
}

// Step is one flow step; exactly one of its kinds is set.
type Step struct {
	Request string          `yaml:"request,omitempty" json:"request,omitempty"`
	Name    string          `yaml:"name,omitempty" json:"name,omitempty"`
	Once    bool            `yaml:"once,omitempty" json:"once,omitempty"`
	Hold    Duration        `yaml:"hold,omitempty" json:"hold,omitempty"`
	Send    []model.Message `yaml:"send,omitempty" json:"send,omitempty"`

	Think *Think `yaml:"think,omitempty" json:"think,omitempty"`

	Group string `yaml:"group,omitempty" json:"group,omitempty"`
	Steps []Step `yaml:"steps,omitempty" json:"steps,omitempty"`

	Loop     *Loop    `yaml:"loop,omitempty" json:"loop,omitempty"`
	If       *If      `yaml:"if,omitempty" json:"if,omitempty"`
	Weighted []Branch `yaml:"weighted,omitempty" json:"weighted,omitempty"`
}

type Think struct {
	Min Duration `yaml:"min" json:"min"`
	Max Duration `yaml:"max" json:"max"`
}

// UnmarshalYAML accepts "2s" or {min: 1s, max: 3s}.
func (t *Think) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var d Duration
		if err := n.Decode(&d); err != nil {
			return err
		}
		t.Min, t.Max = d, d
		return nil
	}
	type plain Think
	if err := n.Decode((*plain)(t)); err != nil {
		return err
	}
	if t.Max < t.Min {
		t.Max = t.Min
	}
	return nil
}

type Loop struct {
	Count int    `yaml:"count,omitempty" json:"count,omitempty"`
	While string `yaml:"while,omitempty" json:"while,omitempty"`
	// Max bounds a while loop (default 1000).
	Max   int    `yaml:"max,omitempty" json:"max,omitempty"`
	Steps []Step `yaml:"steps" json:"steps"`
}

type If struct {
	Expr string `yaml:"expr" json:"expr"`
	Then []Step `yaml:"then" json:"then"`
	Else []Step `yaml:"else,omitempty" json:"else,omitempty"`
}

type Branch struct {
	Weight int    `yaml:"weight" json:"weight"`
	Steps  []Step `yaml:"steps" json:"steps"`
}

// Duration parses "500ms", "2m", "1h30m" or a number of seconds.
type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := parseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(time.Duration(d).String())), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := parseDuration(s)
	*d = Duration(v)
	return err
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(f * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 500ms, 30s, 2m)", s)
	}
	return d, nil
}

// Criteria is a threshold's list of conditions.
type Criteria []Criterion

// Criterion is "p(95)<500" or {threshold: "rate<0.01", abortOnFail: true}.
type Criterion struct {
	Threshold      string   `yaml:"threshold" json:"threshold"`
	AbortOnFail    bool     `yaml:"abortOnFail,omitempty" json:"abortOnFail,omitempty"`
	DelayAbortEval Duration `yaml:"delayAbortEval,omitempty" json:"delayAbortEval,omitempty"`
}

func (c *Criterion) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		c.Threshold = n.Value
		return nil
	}
	type plain Criterion
	return n.Decode((*plain)(c))
}

// ParsePlan parses and validates plan YAML. exists reports whether a request
// path exists in the workspace (nil skips that check).
func ParsePlan(data []byte, exists func(path string) bool) (*Plan, error) {
	var p Plan
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid load plan: %w", err)
	}
	if err := p.Validate(exists); err != nil {
		return nil, err
	}
	return &p, nil
}

// ScenarioNames returns scenario names sorted.
func (p *Plan) ScenarioNames() []string {
	names := make([]string, 0, len(p.Scenarios))
	for n := range p.Scenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Validate checks the plan and fills defaults.
func (p *Plan) Validate(exists func(string) bool) error {
	if len(p.Scenarios) == 0 {
		return fmt.Errorf("the plan has no scenarios")
	}
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	for _, name := range p.ScenarioNames() {
		s := p.Scenarios[name]
		if s == nil {
			add("scenario %s: empty", name)
			continue
		}
		where := "scenario " + name
		switch s.Executor {
		case ConstantVUs:
			if s.VUs <= 0 || s.Duration <= 0 {
				add("%s: constant-vus needs vus and duration", where)
			}
		case RampingVUs:
			if len(s.Stages) == 0 {
				add("%s: ramping-vus needs stages", where)
			}
		case PerVUIterations:
			if s.VUs <= 0 || s.Iterations <= 0 {
				add("%s: per-vu-iterations needs vus and iterations", where)
			}
			if s.MaxDuration == 0 {
				s.MaxDuration = Duration(10 * time.Minute)
			}
		case ConstantArrivalRate:
			if s.Rate <= 0 || s.Duration <= 0 {
				add("%s: constant-arrival-rate needs rate and duration", where)
			}
		case RampingArrivalRate:
			if len(s.Stages) == 0 {
				add("%s: ramping-arrival-rate needs stages", where)
			}
		case "":
			add("%s: executor is required", where)
		default:
			add("%s: unknown executor %q", where, s.Executor)
		}
		if s.Executor == ConstantArrivalRate || s.Executor == RampingArrivalRate {
			if s.TimeUnit == 0 {
				s.TimeUnit = Duration(time.Second)
			}
			if s.PreAllocatedVUs <= 0 {
				s.PreAllocatedVUs = 10
			}
			if s.MaxVUs < s.PreAllocatedVUs {
				s.MaxVUs = s.PreAllocatedVUs
			}
		}
		for _, st := range s.Stages {
			if st.Duration <= 0 || st.Target < 0 {
				add("%s: every stage needs a positive duration and a target ≥ 0", where)
				break
			}
		}
		if s.GracefulStop == 0 {
			s.GracefulStop = Duration(30 * time.Second)
		}
		if s.GracefulRampDown == 0 {
			s.GracefulRampDown = Duration(30 * time.Second)
		}
		if len(s.Flow) == 0 {
			add("%s: flow is empty", where)
		}
		validateSteps(s.Flow, where+" flow", exists, add)
		if s.Data != nil {
			switch s.Data.Mode {
			case "", "shared", "per-vu", "random":
			default:
				add("%s: data mode must be shared, per-vu or random", where)
			}
			switch s.Data.OnEnd {
			case "", "recycle", "stop":
			default:
				add("%s: data onEnd must be recycle or stop", where)
			}
		}
	}
	for key, crit := range p.Thresholds {
		if _, _, err := parseThresholdKey(key); err != nil {
			add("threshold %q: %v", key, err)
		}
		for _, c := range crit {
			if _, err := parseCondition(c.Threshold); err != nil {
				add("threshold %q: %v", key, err)
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid load plan:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

func validateSteps(steps []Step, where string, exists func(string) bool, add func(string, ...any)) {
	for i, st := range steps {
		at := fmt.Sprintf("%s step %d", where, i+1)
		kinds := 0
		for _, set := range []bool{st.Request != "", st.Think != nil, st.Group != "", st.Loop != nil, st.If != nil, len(st.Weighted) > 0} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			add("%s: needs exactly one of request, think, group, loop, if, weighted", at)
			continue
		}
		switch {
		case st.Request != "":
			if exists != nil && !exists(st.Request) {
				add("%s: no request at %q", at, st.Request)
			}
		case st.Group != "":
			validateSteps(st.Steps, at+" ("+st.Group+")", exists, add)
		case st.Loop != nil:
			if st.Loop.Count <= 0 && st.Loop.While == "" {
				add("%s: loop needs count or while", at)
			}
			if st.Loop.While != "" {
				checkExpr(st.Loop.While, at, add)
			}
			validateSteps(st.Loop.Steps, at+" loop", exists, add)
		case st.If != nil:
			checkExpr(st.If.Expr, at, add)
			validateSteps(st.If.Then, at+" then", exists, add)
			validateSteps(st.If.Else, at+" else", exists, add)
		case len(st.Weighted) > 0:
			for j, b := range st.Weighted {
				if b.Weight <= 0 {
					add("%s branch %d: weight must be positive", at, j+1)
				}
				validateSteps(b.Steps, fmt.Sprintf("%s branch %d", at, j+1), exists, add)
			}
		}
	}
}

func checkExpr(src, at string, add func(string, ...any)) {
	if strings.TrimSpace(src) == "" {
		add("%s: expression is empty", at)
		return
	}
	if _, err := expr.Compile(src, expr.AllowUndefinedVariables()); err != nil {
		add("%s: invalid expression %q: %v", at, src, err)
	}
}
