// Package jmeter imports Apache JMeter test plans (.jmx): HTTP samplers
// become requests (one folder per thread group) and each thread group a
// scenario of one load test, with controllers, timers, assertions,
// extractors and CSV data mapped onto Pyro's flow. See docs/import-export.md.
package jmeter

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"gopkg.in/yaml.v3"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/model"
)

// Looks reports whether data is a JMeter test plan.
func Looks(data []byte) bool {
	head := data
	if len(head) > 2048 {
		head = head[:2048]
	}
	return bytes.Contains(head, []byte("<jmeterTestPlan"))
}

type converter struct {
	b       *importer.Bundle
	plan    *load.Plan
	vars    []model.Variable
	taken   map[string]map[string]bool // request slugs by folder
	folder  string
	where   string // thread group, for warnings
	scripts int
	rps     float64 // constant throughput timer, requests/s
}

// scope is what JMeter applies to every sampler below a point in the tree.
type scope struct {
	headers  []model.KeyValue
	defaults *node
	timers   []load.Think
	asserts  []model.Assertion
	extract  []model.Extractor
	auth     *model.Auth
}

// Parse converts a .jmx file. name (optional, e.g. the file name) is used
// when the plan has no name.
func Parse(name string, data []byte) (*importer.Bundle, error) {
	var root node
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("not a valid JMeter file: %w", err)
	}
	if root.tag() != "jmeterTestPlan" || len(root.Children) == 0 {
		return nil, errors.New("not a JMeter test plan")
	}
	top := tree(&root.Children[0])
	if len(top) == 0 || top[0].el.class() != "TestPlan" {
		return nil, errors.New("not a JMeter test plan: no TestPlan element")
	}
	tp := top[0]
	title := strings.TrimSpace(tp.el.name())
	if title == "" || title == "Test Plan" {
		if name != "" {
			title = name
		} else if title == "" {
			title = "JMeter import"
		}
	}
	c := &converter{
		b: &importer.Bundle{
			Collection: &model.Collection{Name: title, Description: tp.el.prop("TestPlan.comments")},
			Slug:       importer.Slugify(title),
			Folders:    map[string]*model.Collection{},
			LoadTests:  map[string][]byte{},
			DataFiles:  map[string]importer.DataFile{},
			Report:     importer.Report{Format: "jmeter", Collection: title, Warnings: []importer.Warning{}},
		},
		plan:  &load.Plan{Name: title, Scenarios: map[string]*load.Scenario{}},
		taken: map[string]map[string]bool{"": {}},
	}
	if args := tp.el.element("TestPlan.user_defined_variables"); args != nil {
		c.addVars(args)
	}
	// Test-plan level config applies to every thread group.
	var base scope
	var groups []item
	for _, it := range tp.kids {
		if !it.el.enabled() {
			continue
		}
		if isThreadGroup(it.el.class()) {
			groups = append(groups, it)
			continue
		}
		c.config(it, &base)
	}
	if len(groups) == 0 {
		return nil, errors.New("the test plan has no enabled thread groups")
	}
	var order []string
	for _, g := range groups {
		if slug := c.threadGroup(g, base); slug != "" {
			order = append(order, slug)
		}
	}
	if len(order) > 1 {
		c.b.Collection.Order = order
	}
	c.b.Collection.Variables = c.vars
	c.sharedBaseURL()
	if c.rps > 0 {
		c.plan.MaxRPS = c.rps
	}
	if c.scripts > 0 {
		c.warn("", "jmeter.script", fmt.Sprintf("%d JSR223/BeanShell element(s) skipped: Groovy and BeanShell can't run in Pyro; rewrite them as script steps (JavaScript) if needed", c.scripts))
	}
	if len(c.plan.Scenarios) > 0 {
		out, err := yaml.Marshal(c.plan)
		if err != nil {
			return nil, err
		}
		c.b.LoadTests[c.b.Slug] = out
	}
	c.b.Report.Slug = c.b.Slug
	if len(c.b.Requests) == 0 {
		return nil, errors.New("the test plan has no enabled HTTP requests")
	}
	return c.b, nil
}

func isThreadGroup(class string) bool {
	switch class {
	case "ThreadGroup", "SetupThreadGroup", "PostThreadGroup",
		"com.blazemeter.jmeter.threads.concurrency.ConcurrencyThreadGroup",
		"com.blazemeter.jmeter.threads.arrivals.ArrivalsThreadGroup",
		"kg.apc.jmeter.threads.UltimateThreadGroup", "kg.apc.jmeter.threads.SteppingThreadGroup":
		return true
	}
	return false
}

func (c *converter) warn(where, code, detail string) { c.b.Report.Warn(where, code, detail) }

// --- values ------------------------------------------------------------------

var (
	fnRe  = regexp.MustCompile(`\$\{(__[A-Za-z0-9]+)\(([^)]*)\)\}`)
	varRe = regexp.MustCompile(`\$\{([A-Za-z_][\w.-]*)\}`)
)

// value converts JMeter ${var} and common functions to Pyro {{var}}.
func (c *converter) value(s string) string {
	s = fnRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := fnRe.FindStringSubmatch(m)
		args := strings.Split(sub[2], ",")
		switch strings.ToLower(sub[1]) {
		case "__uuid":
			return "{{$guid}}"
		case "__random", "__randomstring":
			return "{{$randomInt}}"
		case "__time":
			return "{{$timestamp}}"
		case "__p", "__property":
			if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
				return "{{" + strings.TrimSpace(args[0]) + "}}"
			}
		}
		c.warn(c.where, "jmeter.function", fmt.Sprintf("JMeter function %s isn't supported; left as written", sub[1]))
		return m
	})
	return varRe.ReplaceAllString(s, "{{$1}}")
}

// number reads a numeric property; ${__P(name,default)} gives the default.
func (c *converter) number(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if m := fnRe.FindStringSubmatch(s); m != nil {
		args := strings.Split(m[2], ",")
		if len(args) > 1 {
			s = strings.TrimSpace(args[1])
			c.warn(c.where, "jmeter.property", fmt.Sprintf("%s used its default %s (properties aren't passed to Pyro)", m[0], s))
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if s != "" {
		c.warn(c.where, "jmeter.number", fmt.Sprintf("%q isn't a number; used %g", s, def))
	}
	return def
}

func secs(f float64) load.Duration { return load.Duration(time.Duration(f * float64(time.Second))) }
func millis(f float64) load.Duration {
	return load.Duration(time.Duration(f * float64(time.Millisecond)))
}

func (c *converter) addVars(args *node) {
	for _, a := range args.collection("Arguments.arguments") {
		name := a.prop("Argument.name")
		if name == "" {
			continue
		}
		dup := false
		for _, v := range c.vars {
			dup = dup || v.Key == name
		}
		if !dup {
			c.vars = append(c.vars, model.Variable{Key: name, Value: c.value(a.prop("Argument.value"))})
		}
	}
}

// --- config elements -----------------------------------------------------------

// config applies a non-sampler, non-controller element to the scope; it
// reports whether it was one.
func (c *converter) config(it item, sc *scope) bool {
	el := it.el
	switch el.class() {
	case "HeaderManager":
		for _, h := range el.collection("HeaderManager.headers") {
			k := h.prop("Header.name")
			if k == "" {
				continue
			}
			sc.headers = setHeader(sc.headers, k, c.value(h.prop("Header.value")))
		}
	case "ConfigTestElement":
		if el.guiclass() == "HttpDefaultsGui" || el.prop("HTTPSampler.domain") != "" {
			sc.defaults = el
		}
	case "Arguments":
		c.addVars(el)
	case "ConstantTimer":
		d := millis(c.number(el.prop("ConstantTimer.delay"), 0))
		sc.timers = append(sc.timers, load.Think{Min: d, Max: d})
	case "UniformRandomTimer":
		off := c.number(el.prop("ConstantTimer.delay"), 0)
		rng := c.number(el.prop("RandomTimer.range"), 0)
		sc.timers = append(sc.timers, load.Think{Min: millis(off), Max: millis(off + rng)})
	case "GaussianRandomTimer", "PoissonRandomTimer":
		off := c.number(el.prop("ConstantTimer.delay"), 0)
		dev := c.number(el.prop("RandomTimer.range"), 0)
		sc.timers = append(sc.timers, load.Think{Min: millis(max(off-dev, 0)), Max: millis(off + dev)})
		c.warn(c.where, "jmeter.timer", el.class()+" approximated as a uniform think time")
	case "ConstantThroughputTimer":
		perMin := c.number(el.prop("throughput"), 0)
		if perMin > 0 && (c.rps == 0 || perMin/60 < c.rps) {
			c.rps = perMin / 60
		}
		c.warn(c.where, "jmeter.throughput", fmt.Sprintf("Constant Throughput Timer became maxRPS %.4g for the whole test (Pyro caps across all VUs)", perMin/60))
	case "ResponseAssertion", "JSONPathAssertion", "DurationAssertion":
		sc.asserts = append(sc.asserts, c.assertion(el)...)
	case "JSONPostProcessor", "RegexExtractor", "BoundaryExtractor":
		sc.extract = append(sc.extract, c.extractor(el)...)
	case "AuthManager":
		for _, a := range el.collection("AuthManager.auth_list") {
			mech := strings.ToUpper(a.prop("Authorization.mechanism"))
			if mech == "" || mech == "BASIC" || mech == "BASIC_DIGEST" || mech == "DIGEST" {
				typ := "basic"
				if mech == "DIGEST" {
					typ = "digest"
				}
				sc.auth = &model.Auth{Type: typ, Params: map[string]string{"username": c.value(a.prop("Authorization.username")), "password": c.value(a.prop("Authorization.password"))}}
				if len(el.collection("AuthManager.auth_list")) > 1 {
					c.warn(c.where, "jmeter.auth", "several HTTP Authorization entries; the first was used for every request")
				}
				break
			}
			c.warn(c.where, "jmeter.auth", "Kerberos authorization isn't supported")
		}
	case "CookieManager", "CacheManager", "DNSCacheManager", "KeystoreConfig":
		// Pyro keeps cookies per VU and has no HTTP cache; nothing to do.
	case "ResultCollector", "BackendListener", "Summariser", "DebugPostProcessor":
		// Listeners: Pyro has its own results.
	case "JSR223PreProcessor", "JSR223PostProcessor", "JSR223Assertion", "JSR223Timer", "JSR223Listener",
		"BeanShellPreProcessor", "BeanShellPostProcessor", "BeanShellAssertion", "BeanShellTimer", "BeanShellListener":
		c.scripts++
	case "CSVDataSet":
		// Read by threadGroup; ignored elsewhere.
	default:
		return false
	}
	return true
}

func setHeader(hs []model.KeyValue, k, v string) []model.KeyValue {
	out := make([]model.KeyValue, 0, len(hs)+1)
	for _, h := range hs {
		if !strings.EqualFold(h.Key, k) {
			out = append(out, h)
		}
	}
	return append(out, model.KeyValue{Key: k, Value: v})
}

func (s scope) clone() scope {
	s.headers = append([]model.KeyValue(nil), s.headers...)
	s.timers = append([]load.Think(nil), s.timers...)
	s.asserts = append([]model.Assertion(nil), s.asserts...)
	s.extract = append([]model.Extractor(nil), s.extract...)
	return s
}

// --- thread groups -----------------------------------------------------------------

func (c *converter) threadGroup(g item, base scope) string {
	el := g.el
	name := strings.TrimSpace(el.name())
	if name == "" {
		name = "Thread Group"
	}
	slug := importer.Unique(importer.Slugify(name), c.taken[""])
	c.folder, c.where = slug, name
	c.taken[slug] = map[string]bool{}
	c.b.Folders[slug] = &model.Collection{Name: name, Description: el.prop("TestPlan.comments")}
	c.b.Report.Folders++

	sc := &load.Scenario{}
	threads := int(c.number(el.prop("ThreadGroup.num_threads"), 1))
	ramp := c.number(el.prop("ThreadGroup.ramp_time"), 0)
	switch el.class() {
	case "SetupThreadGroup", "PostThreadGroup":
		c.warn(name, "jmeter.setup", "setUp/tearDown thread groups ran as ordinary scenarios; move one-time steps under once: true if needed")
	case "com.blazemeter.jmeter.threads.concurrency.ConcurrencyThreadGroup":
		target := c.number(el.prop("TargetLevel"), 1)
		rampUp := c.number(el.prop("RampUp"), 0)
		hold := c.number(el.prop("Hold"), 60)
		unit := 1.0
		if el.prop("Unit") == "M" {
			unit = 60
		}
		sc.Executor = load.RampingVUs
		sc.Stages = []load.Stage{{Duration: secs(max(rampUp*unit, 1)), Target: target}, {Duration: secs(hold * unit), Target: target}}
		if steps := c.number(el.prop("Steps"), 0); steps > 1 {
			c.warn(name, "jmeter.steps", "Concurrency Thread Group steps became a smooth ramp")
		}
	case "com.blazemeter.jmeter.threads.arrivals.ArrivalsThreadGroup":
		target := c.number(el.prop("TargetLevel"), 1)
		unit := 1.0
		if el.prop("Unit") == "M" {
			unit = 60
		}
		sc.Executor = load.RampingArrivalRate
		sc.Stages = []load.Stage{{Duration: secs(max(c.number(el.prop("RampUp"), 0)*unit, 1)), Target: target}, {Duration: secs(c.number(el.prop("Hold"), 60) * unit), Target: target}}
		sc.PreAllocatedVUs, sc.MaxVUs = 10, max(int(target)*10, 100)
	case "kg.apc.jmeter.threads.UltimateThreadGroup", "kg.apc.jmeter.threads.SteppingThreadGroup":
		c.warn(name, "jmeter.threadgroup", el.class()+" isn't translated; the scenario runs 10 VUs for 1 minute: adjust its stages")
		sc.Executor, sc.VUs, sc.Duration = load.ConstantVUs, 10, secs(60)
	}
	if sc.Executor == "" {
		loops := -1.0
		if lc := el.element("ThreadGroup.main_controller"); lc != nil && !lc.boolProp("LoopController.continue_forever") {
			loops = c.number(lc.prop("LoopController.loops"), 1)
		}
		duration := 0.0
		if el.boolProp("ThreadGroup.scheduler") {
			duration = c.number(el.prop("ThreadGroup.duration"), 0)
		}
		switch {
		case loops > 0 && duration == 0:
			sc.Executor, sc.VUs, sc.Iterations = load.PerVUIterations, threads, int(loops)
			if ramp > 0 {
				c.warn(name, "jmeter.ramp", fmt.Sprintf("ramp-up of %gs ignored: a fixed number of loops per thread runs as per-vu-iterations, which starts all VUs at once", ramp))
			}
		case ramp > 0:
			if duration == 0 {
				duration = max(ramp, 60) + ramp
				c.warn(name, "jmeter.duration", fmt.Sprintf("the thread group loops forever with no duration; it runs for %gs", duration))
			}
			sc.Executor = load.RampingVUs
			sc.Stages = []load.Stage{{Duration: secs(ramp), Target: float64(threads)}}
			if hold := duration - ramp; hold > 0 {
				sc.Stages = append(sc.Stages, load.Stage{Duration: secs(hold), Target: float64(threads)})
			}
			if loops > 0 {
				c.warn(name, "jmeter.loops", "loop count combined with a duration: Pyro runs for the duration")
			}
		default:
			if duration == 0 {
				duration = 60
				c.warn(name, "jmeter.duration", "the thread group loops forever with no duration; it runs for 60s")
			}
			sc.Executor, sc.VUs, sc.Duration = load.ConstantVUs, threads, secs(duration)
		}
		if d := c.number(el.prop("ThreadGroup.delay"), 0); d > 0 && el.boolProp("ThreadGroup.scheduler") {
			sc.StartTime = secs(d)
		}
	}

	scope := base.clone()
	for _, it := range g.kids {
		if it.el.enabled() && it.el.class() == "CSVDataSet" {
			c.csv(it.el, sc)
		}
	}
	sc.Flow = c.steps(g.kids, scope)
	if len(sc.Flow) == 0 {
		c.warn(name, "jmeter.empty", "no HTTP requests: the thread group was left out of the load test")
		return slug
	}
	c.plan.Scenarios[strings.ReplaceAll(slug, "-", "_")] = sc
	return slug
}

func (c *converter) csv(el *node, sc *load.Scenario) {
	if sc.Data != nil {
		c.warn(c.where, "jmeter.csv", "several CSV Data Sets: only the first is used (Pyro feeds one data file per scenario)")
		return
	}
	file := c.value(el.prop("filename"))
	if file == "" {
		return
	}
	dst := "data/" + path.Base(strings.ReplaceAll(file, `\`, "/"))
	df := importer.DataFile{Source: file}
	if names := strings.TrimSpace(el.prop("variableNames")); names != "" && !el.boolProp("ignoreFirstLine") {
		df.Header = strings.ReplaceAll(names, " ", "")
	}
	if d := el.prop("delimiter"); d != "" && d != "," {
		c.warn(c.where, "jmeter.csv", fmt.Sprintf("CSV delimiter %q: Pyro reads comma-separated files; convert %s", d, dst))
	}
	c.b.DataFiles[dst] = df
	data := &load.Data{File: dst}
	if el.prop("recycle") == "false" && el.boolProp("stopThread") {
		data.OnEnd = "stop"
	}
	if mode := el.prop("shareMode"); mode == "shareMode.thread" {
		c.warn(c.where, "jmeter.csv", "CSV sharing mode 'current thread' became shared (Pyro's per-vu mode gives each VU one fixed row)")
	}
	sc.Data = data
	c.warn(c.where, "jmeter.csv", fmt.Sprintf("the load test reads %s (from %s); copy the file there if the import didn't", dst, file))
}

// --- flow ---------------------------------------------------------------------------

// steps converts one level of a thread group or controller.
func (c *converter) steps(items []item, parent scope) []load.Step {
	sc := parent.clone()
	for _, it := range items {
		if it.el.enabled() {
			c.config(it, &sc)
		}
	}
	var out []load.Step
	for _, it := range items {
		el := it.el
		if !el.enabled() {
			continue
		}
		switch el.class() {
		case "HTTPSamplerProxy", "HTTPSampler", "HTTPSampler2":
			out = append(out, c.sampler(it, sc)...)
		case "TransactionController":
			if kids := c.steps(it.kids, sc); len(kids) > 0 {
				out = append(out, load.Step{Group: nonEmpty(el.name(), "Transaction"), Steps: kids})
			}
		case "GenericController", "ThroughputController", "InterleaveControl", "RunTime", "SwitchController":
			if el.class() != "GenericController" {
				c.warn(c.where, "jmeter.controller", el.class()+" runs its children in order (no equivalent)")
			}
			out = append(out, c.steps(it.kids, sc)...)
		case "LoopController":
			kids := c.steps(it.kids, sc)
			n := int(c.number(el.prop("LoopController.loops"), 1))
			if n < 0 {
				n = 1
				c.warn(c.where, "jmeter.loop", "an infinite inner loop runs once per iteration")
			}
			if len(kids) > 0 {
				out = append(out, load.Step{Loop: &load.Loop{Count: max(n, 1), Steps: kids}})
			}
		case "WhileController":
			kids := c.steps(it.kids, sc)
			cond := c.condition(el.prop("WhileController.condition"))
			if cond == "" {
				cond = "true"
				c.warn(c.where, "jmeter.while", "While Controller without a condition (loop until a sampler fails) became a loop of at most 1000 iterations; set loop.while")
			}
			if len(kids) > 0 {
				out = append(out, load.Step{Loop: &load.Loop{While: cond, Max: 1000, Steps: kids}})
			}
		case "IfController":
			kids := c.steps(it.kids, sc)
			cond := c.condition(el.prop("IfController.condition"))
			if cond == "" {
				cond = "true"
			}
			if len(kids) > 0 {
				out = append(out, load.Step{If: &load.If{Expr: cond, Then: kids}})
			}
		case "OnceOnlyController":
			for _, s := range c.steps(it.kids, sc) {
				if s.Request != "" {
					s.Once = true
				} else {
					c.warn(c.where, "jmeter.once", "Once Only Controller around a non-request step: only its requests run once")
				}
				out = append(out, s)
			}
		case "RandomController", "RandomOrderController":
			var branches []load.Branch
			for _, k := range it.kids {
				if s := c.steps([]item{k}, sc); len(s) > 0 {
					branches = append(branches, load.Branch{Weight: 1, Steps: s})
				}
			}
			if el.class() == "RandomOrderController" {
				c.warn(c.where, "jmeter.controller", "Random Order Controller became a random choice of one child per iteration")
			}
			if len(branches) > 0 {
				out = append(out, load.Step{Weighted: branches})
			}
		case "TestAction":
			if d := c.number(el.prop("ActionProcessor.duration"), 0); d > 0 {
				out = append(out, load.Step{Think: &load.Think{Min: millis(d), Max: millis(d)}})
			}
		case "ModuleController", "IncludeController":
			c.warn(c.where, "jmeter.controller", el.class()+" isn't supported; its target wasn't imported here")
		case "JSR223Sampler", "BeanShellSampler":
			c.scripts++
		default:
			if !isConfig(el.class()) && !strings.HasSuffix(el.class(), "Sampler") {
				continue
			}
			if strings.HasSuffix(el.class(), "Sampler") {
				c.warn(c.where, "jmeter.sampler", el.class()+" isn't supported (HTTP samplers only)")
			}
		}
	}
	return out
}

func isConfig(class string) bool {
	switch class {
	case "HeaderManager", "ConfigTestElement", "Arguments", "CSVDataSet", "CookieManager", "CacheManager", "AuthManager":
		return true
	}
	return false
}

func nonEmpty(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

var (
	jexlRe     = regexp.MustCompile(`^\$\{__(?:jexl[23]?|groovy|javaScript)\((.*?)(?:,[^,()]*)?\)\}$`)
	quotedVar  = regexp.MustCompile(`["']\$\{([A-Za-z_][\w]*)\}["']`)
	quotedNum  = regexp.MustCompile(`(==|!=|<=|>=|<|>)\s*["'](-?\d+(?:\.\d+)?)["']`)
	equalsCall = regexp.MustCompile(`\.equals\(\s*(["'][^"']*["'])\s*\)`)
)

// condition converts a JMeter If/While condition to a Pyro expression,
// or returns "" (with a warning) when it can't.
func (c *converter) condition(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	orig := s
	if m := jexlRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = quotedVar.ReplaceAllString(s, "$1")
	s = varRe.ReplaceAllString(s, "$1")
	s = equalsCall.ReplaceAllString(s, " == $1")
	s = quotedNum.ReplaceAllString(s, "$1 $2")
	s = strings.ReplaceAll(s, "'", `"`)
	// Method calls and JMeter's script objects (vars, props, ctx) only mean
	// something in Groovy/JavaScript; Pyro's expressions compare variables.
	scripty := regexp.MustCompile(`\b(vars|props|ctx|prev|sampler|log)\.|\w\(`).MatchString(s)
	if _, err := expr.Compile(s, expr.AllowUndefinedVariables()); err != nil || scripty {
		c.warn(c.where, "jmeter.condition", fmt.Sprintf("condition %q couldn't be converted; it was set to true: rewrite it as an expression", orig))
		return "true"
	}
	if s != orig {
		c.warn(c.where, "jmeter.condition", fmt.Sprintf("condition %q became %q: check it", orig, s))
	}
	return s
}

// --- samplers ------------------------------------------------------------------------

func (c *converter) sampler(it item, outer scope) []load.Step {
	el := it.el
	sc := outer.clone()
	for _, k := range it.kids { // the sampler's own headers, timers, assertions, extractors
		if k.el.enabled() {
			c.config(k, &sc)
		}
	}
	r := &model.Request{Kind: model.KindHTTP, Name: nonEmpty(el.name(), "Request")}
	r.Method = strings.ToUpper(nonEmpty(el.prop("HTTPSampler.method"), "GET"))
	r.URL = c.sampleURL(el, sc.defaults)
	r.Headers = sc.headers
	r.Auth = sc.auth
	r.Assertions = sc.asserts
	r.Extract = sc.extract

	var args []node
	if a := el.element("HTTPsampler.Arguments"); a != nil {
		args = a.collection("Arguments.arguments")
	}
	ctype := ""
	for _, h := range r.Headers {
		if strings.EqualFold(h.Key, "Content-Type") {
			ctype = strings.ToLower(h.Value)
		}
	}
	switch {
	case el.boolProp("HTTPSampler.postBodyRaw"):
		body := ""
		for _, a := range args {
			body += a.prop("Argument.value")
		}
		body = c.value(body)
		t := model.BodyText
		switch {
		case strings.Contains(ctype, "json") || (ctype == "" && looksJSON(body)):
			t = model.BodyJSON
		case strings.Contains(ctype, "xml"):
			t = model.BodyXML
		}
		r.Body = &model.Body{Type: t, Content: body}
	case len(args) > 0 && (r.Method == "GET" || r.Method == "DELETE" || r.Method == "HEAD" || r.Method == "OPTIONS"):
		for _, a := range args {
			if n := a.prop("Argument.name"); n != "" {
				r.Query = append(r.Query, model.KeyValue{Key: c.value(n), Value: c.value(a.prop("Argument.value"))})
			}
		}
	case len(args) > 0:
		t := model.BodyForm
		if el.boolProp("HTTPSampler.DO_MULTIPART_POST") {
			t = model.BodyMultipart
		}
		body := &model.Body{Type: t}
		for _, a := range args {
			if n := a.prop("Argument.name"); n != "" {
				body.Fields = append(body.Fields, model.KeyValue{Key: c.value(n), Value: c.value(a.prop("Argument.value"))})
			}
		}
		r.Body = body
	}
	if files := el.element("HTTPsampler.Files"); files != nil {
		for _, f := range files.collection("HTTPFileArgs.files") {
			p := c.value(f.prop("File.path"))
			if p == "" {
				continue
			}
			if r.Body == nil || r.Body.Type != model.BodyMultipart {
				if r.Body == nil {
					r.Body = &model.Body{Type: model.BodyMultipart}
				} else {
					r.Body.Type = model.BodyMultipart
				}
			}
			r.Body.Fields = append(r.Body.Fields, model.KeyValue{Key: nonEmpty(f.prop("File.paramname"), "file"), Value: p, File: true})
			c.warn(c.where, "body.file", fmt.Sprintf("%s uploads %s; copy it into the workspace", r.Name, p))
		}
	}
	slug := importer.Unique(importer.Slugify(r.Name), c.taken[c.folder])
	rel := c.folder + "/" + slug
	c.b.Requests = append(c.b.Requests, importer.Request{Path: rel, Request: r})
	c.b.Report.Requests++
	f := c.b.Folders[c.folder]
	f.Order = append(f.Order, slug)

	var steps []load.Step
	if len(sc.timers) > 0 {
		// JMeter runs every timer in scope before the sampler; their
		// delays add up.
		var total load.Think
		for _, t := range sc.timers {
			total.Min += t.Min
			total.Max += t.Max
		}
		steps = append(steps, load.Step{Think: &total})
	}
	return append(steps, load.Step{Request: importer.CollectionRef + rel, Name: r.Name})
}

func looksJSON(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

// sampleURL builds the request URL from the sampler, falling back to the
// HTTP Request Defaults.
func (c *converter) sampleURL(el, defaults *node) string {
	get := func(k string) string {
		v := el.prop("HTTPSampler." + k)
		if v == "" && defaults != nil {
			v = defaults.prop("HTTPSampler." + k)
		}
		return v
	}
	p := el.prop("HTTPSampler.path")
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return c.value(p)
	}
	domain := get("domain")
	if domain == "" {
		c.warn(c.where, "jmeter.url", fmt.Sprintf("%s has no server name (and there are no HTTP Request Defaults); set its URL", el.name()))
		return c.value(p)
	}
	proto := nonEmpty(get("protocol"), "http")
	port := get("port")
	u := proto + "://" + domain
	if port != "" && !(proto == "http" && port == "80") && !(proto == "https" && port == "443") {
		u += ":" + port
	}
	if p == "" {
		p = get("path")
	}
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return c.value(u + p)
}

// sharedBaseURL moves a scheme+host common to every request into a
// baseUrl collection variable.
func (c *converter) sharedBaseURL() {
	origin := ""
	for _, r := range c.b.Requests {
		u, err := url.Parse(r.Request.URL)
		if err != nil || u.Host == "" {
			return
		}
		o := u.Scheme + "://" + u.Host
		if origin != "" && o != origin {
			return
		}
		origin = o
	}
	if origin == "" {
		return
	}
	for _, v := range c.vars {
		if v.Key == "baseUrl" {
			return
		}
	}
	for _, r := range c.b.Requests {
		r.Request.URL = "{{baseUrl}}" + strings.TrimPrefix(r.Request.URL, origin)
	}
	c.vars = append([]model.Variable{{Key: "baseUrl", Value: origin}}, c.vars...)
	c.b.Collection.Variables = c.vars
}

// --- assertions and extractors -------------------------------------------------------------

func (c *converter) assertion(el *node) []model.Assertion {
	switch el.class() {
	case "DurationAssertion":
		if d := c.number(el.prop("DurationAssertion.duration"), 0); d > 0 {
			return []model.Assertion{{Type: "duration", Op: "lte", Value: strconv.FormatFloat(d, 'f', -1, 64)}}
		}
	case "JSONPathAssertion":
		p := el.prop("JSON_PATH")
		if p == "" {
			return nil
		}
		if !el.boolProp("JSONVALIDATION") {
			return []model.Assertion{{Type: "jsonpath", Path: p, Op: "exists"}}
		}
		if el.boolProp("INVERT") {
			return []model.Assertion{{Type: "jsonpath", Path: p, Op: "neq", Value: c.value(el.prop("EXPECTED_VALUE"))}}
		}
		op := "eq"
		if el.boolProp("ISREGEX") {
			op = "matches"
		}
		return []model.Assertion{{Type: "jsonpath", Path: p, Op: op, Value: c.value(el.prop("EXPECTED_VALUE"))}}
	case "ResponseAssertion":
		field := el.prop("Assertion.test_field")
		mode, _ := strconv.Atoi(el.prop("Assertion.test_type"))
		not := mode&4 != 0
		if mode&32 != 0 {
			c.warn(c.where, "jmeter.assertion", "Response Assertion with OR: each pattern became its own assertion (all must pass)")
		}
		strs := el.collection("Asserion.test_strings") // sic: JMeter's spelling
		if strs == nil {
			strs = el.collection("Assertion.test_strings")
		}
		var out []model.Assertion
		for _, s := range strs {
			v := c.value(strings.TrimSpace(s.Text))
			switch {
			case field == "Assertion.response_code":
				op := "eq"
				if not {
					op = "neq"
				}
				if mode&(1|2) != 0 && !regexp.MustCompile(`^\d+$`).MatchString(v) {
					op = "matches"
					if not {
						c.warn(c.where, "jmeter.assertion", "negated status-code pattern skipped")
						continue
					}
				}
				out = append(out, model.Assertion{Type: "status", Op: op, Value: v})
			case field == "Assertion.response_data" || field == "Assertion.response_data_as_document" || field == "":
				switch {
				case not && mode&8 != 0:
					out = append(out, model.Assertion{Type: "body", Op: "neq", Value: v})
				case not:
					c.warn(c.where, "jmeter.assertion", fmt.Sprintf("negated text assertion %q skipped (no 'does not contain' check)", v))
				case mode&8 != 0:
					out = append(out, model.Assertion{Type: "body", Op: "eq", Value: v})
				case mode&16 != 0:
					out = append(out, model.Assertion{Type: "body", Op: "contains", Value: v})
				case mode&1 != 0:
					out = append(out, model.Assertion{Type: "body", Op: "matches", Value: "^(?s:" + v + ")$"})
				default:
					out = append(out, model.Assertion{Type: "body", Op: "matches", Value: v})
				}
			case field == "Assertion.response_headers":
				if k, val, ok := strings.Cut(v, ":"); ok && !not {
					out = append(out, model.Assertion{Type: "header", Path: strings.TrimSpace(k), Op: "contains", Value: strings.TrimSpace(val)})
				} else {
					c.warn(c.where, "jmeter.assertion", fmt.Sprintf("header assertion %q skipped (needs 'Name: value')", v))
				}
			default:
				c.warn(c.where, "jmeter.assertion", fmt.Sprintf("Response Assertion on %s skipped", strings.TrimPrefix(field, "Assertion.")))
			}
		}
		return out
	}
	return nil
}

func (c *converter) extractor(el *node) []model.Extractor {
	switch el.class() {
	case "JSONPostProcessor":
		names := strings.Split(el.prop("JSONPostProcessor.referenceNames"), ";")
		paths := strings.Split(el.prop("JSONPostProcessor.jsonPathExprs"), ";")
		var out []model.Extractor
		for i, n := range names {
			n = strings.TrimSpace(n)
			if n == "" || i >= len(paths) {
				continue
			}
			out = append(out, model.Extractor{Var: n, From: "jsonpath", Path: strings.TrimSpace(paths[i])})
		}
		if m := el.prop("JSONPostProcessor.match_numbers"); m != "" && m != "1" && m != "0" {
			c.warn(c.where, "jmeter.extractor", "JSON Extractor match number other than the first: Pyro takes the first match")
		}
		return out
	case "RegexExtractor":
		n := el.prop("RegexExtractor.refname")
		if n == "" {
			return nil
		}
		if t := el.prop("RegexExtractor.template"); t != "" && t != "$1$" {
			c.warn(c.where, "jmeter.extractor", fmt.Sprintf("regex template %s: Pyro captures group 1", t))
		}
		if el.prop("RegexExtractor.useHeaders") == "true" {
			c.warn(c.where, "jmeter.extractor", "regex on headers: Pyro's regex extractor reads the body")
		}
		return []model.Extractor{{Var: n, From: "regex", Path: el.prop("RegexExtractor.regex")}}
	case "BoundaryExtractor":
		n := el.prop("BoundaryExtractor.refname")
		if n == "" {
			return nil
		}
		re := regexp.QuoteMeta(el.prop("BoundaryExtractor.lboundary")) + "([\\s\\S]*?)" + regexp.QuoteMeta(el.prop("BoundaryExtractor.rboundary"))
		return []model.Extractor{{Var: n, From: "regex", Path: re}}
	}
	return nil
}
