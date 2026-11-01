package load

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/codeen15/pyro/pkg/engine"
	"github.com/codeen15/pyro/pkg/engine/metrics"
	"github.com/codeen15/pyro/pkg/engine/model"
	"github.com/codeen15/pyro/pkg/engine/protocol"
	"github.com/codeen15/pyro/pkg/engine/script"
	"github.com/codeen15/pyro/pkg/engine/vars"
)

// Target is a resolved request reference.
type Target struct {
	Path    string              `json:"path"`
	Request *model.Request      `json:"request"`
	Chain   []*model.Collection `json:"chain,omitempty"`
}

var errDataExhausted = errors.New("data file exhausted")

// vu is one virtual user. It is only ever used by one goroutine at a time.
type vu struct {
	id       int
	scenario string
	sc       *Scenario
	t        *Test
	eng      *engine.Engine
	vars     *vars.Set
	collVars map[string]map[string]string // by collection slug, so scripts' changes stick
	buf      *metrics.Buffer
	rnd      *rand.Rand
	feed     *feeder
	// rt is created on first use, so flows without scripts never pay for it.
	rt *script.Runtime

	iteration  int
	onceDone   map[*Step]bool
	lastStatus int
	lastMS     float64
}

func (t *Test) newVU(scenario string, sc *Scenario, feed *feeder) *vu {
	id := int(t.vuSeq.Add(1))
	v := &vu{
		id: id, scenario: scenario, sc: sc, t: t,
		eng:      &engine.Engine{HTTP: t.http.ForVU(), Scripts: &script.Pool{}, Plugins: t.opts.Plugins},
		vars:     newVUVars(t.opts.Environment, t.opts.Globals),
		collVars: map[string]map[string]string{},
		buf:      t.agg.NewBuffer(),
		rnd:      rand.New(rand.NewPCG(uint64(id), uint64(time.Now().UnixNano()))),
		feed:     feed,
		onceDone: map[*Step]bool{},
	}
	t.allocated.Add(1)
	return v
}

// iterate runs the flow once.
func (v *vu) iterate(ctx context.Context) error {
	if v.feed != nil {
		row, err := v.feed.next(v)
		if err != nil {
			return err
		}
		v.vars.Data = row
	}
	start := time.Now()
	v.t.busy.Add(1)
	err := v.steps(ctx, v.sc.Flow, "")
	v.t.busy.Add(-1)
	tags := metrics.Tags{Scenario: v.scenario}
	if ctx.Err() == nil {
		v.buf.AddMany(
			metrics.Sample{Metric: "iterations", Tags: tags, Value: 1},
			metrics.Sample{Metric: "iteration_duration", Tags: tags, Value: ms(time.Since(start))},
		)
	}
	v.iteration++
	return err
}

func (v *vu) steps(ctx context.Context, steps []Step, group string) error {
	for i := range steps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st := &steps[i]
		if st.Once && v.onceDone[st] {
			continue
		}
		if err := v.step(ctx, st, group); err != nil {
			return err
		}
		if st.Once {
			v.onceDone[st] = true
		}
	}
	return nil
}

func (v *vu) step(ctx context.Context, st *Step, group string) error {
	switch {
	case st.Request != "":
		return v.request(ctx, st, group)
	case st.Think != nil:
		d := st.Think.Min.D()
		if span := st.Think.Max.D() - d; span > 0 {
			d += time.Duration(v.rnd.Int64N(int64(span)))
		}
		return sleep(ctx, d)
	case st.Group != "":
		start := time.Now()
		name := st.Group
		if group != "" {
			name = group + "::" + st.Group
		}
		err := v.steps(ctx, st.Steps, name)
		if ctx.Err() == nil {
			v.buf.Add(metrics.Sample{Metric: "group_duration", Tags: metrics.Tags{Scenario: v.scenario, Group: name}, Value: ms(time.Since(start))})
		}
		return err
	case st.Loop != nil:
		if st.Loop.Count > 0 {
			for i := 0; i < st.Loop.Count; i++ {
				if err := v.steps(ctx, st.Loop.Steps, group); err != nil {
					return err
				}
			}
			return nil
		}
		max := st.Loop.Max
		if max <= 0 {
			max = 1000
		}
		for i := 0; i < max; i++ {
			ok, err := v.eval(st.Loop.While)
			if err != nil || !ok {
				return err
			}
			if err := v.steps(ctx, st.Loop.Steps, group); err != nil {
				return err
			}
		}
		return nil
	case st.If != nil:
		ok, err := v.eval(st.If.Expr)
		if err != nil {
			return err
		}
		if ok {
			return v.steps(ctx, st.If.Then, group)
		}
		return v.steps(ctx, st.If.Else, group)
	case len(st.Weighted) > 0:
		total := 0
		for _, b := range st.Weighted {
			total += b.Weight
		}
		pick := v.rnd.IntN(total)
		for _, b := range st.Weighted {
			if pick < b.Weight {
				return v.steps(ctx, b.Steps, group)
			}
			pick -= b.Weight
		}
	}
	return nil
}

// eval evaluates an if/while expression over the VU's variables plus
// `status` and `duration` (ms) of the last response. Numeric strings are
// compared as numbers.
func (v *vu) eval(src string) (bool, error) {
	prog, err := v.t.program(src)
	if err != nil {
		return false, err
	}
	env := map[string]any{}
	for _, m := range []map[string]string{v.vars.Globals, v.vars.Collection, v.vars.Environment, v.vars.Data, v.vars.Runtime} {
		for k, s := range m {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				env[k] = f
			} else {
				env[k] = s
			}
		}
	}
	env["status"] = float64(v.lastStatus)
	env["duration"] = v.lastMS
	env["iteration"] = float64(v.iteration)
	out, err := vm.Run(prog, env)
	if err != nil {
		return false, fmt.Errorf("expression %q: %w", src, err)
	}
	b, _ := out.(bool)
	return b, nil
}

func (v *vu) request(ctx context.Context, st *Step, group string) error {
	target := v.t.targets[st.Request]
	slug := strings.SplitN(target.Path, "/", 2)[0]
	cv := v.collVars[slug]
	if cv == nil {
		cv = map[string]string{}
		if len(target.Chain) > 0 && target.Chain[0] != nil {
			cv = vars.Map(target.Chain[0].Variables)
		}
		v.collVars[slug] = cv
	}
	v.vars.Collection = cv
	v.vars.Folders = v.vars.Folders[:0]
	for i := len(target.Chain) - 1; i >= 1; i-- {
		if c := target.Chain[i]; c != nil && len(c.Variables) > 0 {
			v.vars.Folders = append(v.vars.Folders, vars.Map(c.Variables))
		}
	}
	name := st.Name
	if name == "" {
		name = target.Path
	}
	tags := metrics.Tags{Scenario: v.scenario, Request: name, Group: group}
	in := engine.SendInput{Request: target.Request, Path: target.Path, Chain: target.Chain, Vars: v.vars, Iteration: v.iteration}
	if v.t.scripted[st.Request] {
		if v.rt == nil {
			rt, err := script.New()
			if err != nil {
				return err
			}
			v.rt = rt
		}
		in.Script = v.rt
	}

	if k := target.Request.Kind; k == model.KindWebSocket || k == model.KindSocketIO {
		return v.stream(ctx, st, in, tags)
	}
	res := v.eng.Send(ctx, in)
	if ctx.Err() != nil {
		return ctx.Err() // cancelled mid-request: don't record a bogus failure
	}
	v.recordHTTP(res, tags, name)
	return nil
}

func (v *vu) recordHTTP(res *engine.Result, tags metrics.Tags, name string) {
	if res.Skipped {
		return
	}
	method := "GET"
	if res.Sent != nil && res.Sent.Method != "" {
		method = strings.ToUpper(res.Sent.Method)
	}
	if res.Sent != nil {
		if id, ok := engine.PluginID(res.Sent.Kind); ok {
			method = strings.ToUpper(id) // e.g. TCP, shown where HTTP shows the method
		}
	}
	tags.Method = method
	samples := make([]metrics.Sample, 0, 12)
	failed := 1.0
	if r := res.Response; r != nil {
		tags.Status = strconv.Itoa(r.Status)
		t := r.Timings
		v.lastStatus, v.lastMS = r.Status, ms(t.Total)
		if r.Status < 400 && res.Error == "" {
			failed = 0
		}
		samples = append(samples,
			metrics.Sample{Metric: "http_req_duration", Tags: tags, Value: ms(t.Total)},
			metrics.Sample{Metric: "http_req_blocked", Tags: tags, Value: ms(t.DNS)},
			metrics.Sample{Metric: "http_req_connecting", Tags: tags, Value: ms(t.Connect)},
			metrics.Sample{Metric: "http_req_tls", Tags: tags, Value: ms(t.TLS)},
			metrics.Sample{Metric: "http_req_sending", Tags: tags, Value: ms(t.Send)},
			metrics.Sample{Metric: "http_req_waiting", Tags: tags, Value: ms(t.Wait)},
			metrics.Sample{Metric: "http_req_receiving", Tags: tags, Value: ms(t.Download)},
			metrics.Sample{Metric: "data_received", Tags: tags, Value: float64(r.Size)},
		)
		if r.Status >= 400 {
			v.t.errors.add(fmt.Sprintf("HTTP %d from %s", r.Status, name))
		}
	} else {
		tags.Status = "error"
		v.lastStatus, v.lastMS = 0, 0
	}
	if res.Error != "" {
		v.t.errors.add(res.Error + " (" + name + ")")
	}
	for _, e := range res.ScriptErrors {
		v.t.errors.add(e + " (" + name + ")")
	}
	samples = append(samples,
		metrics.Sample{Metric: "http_reqs", Tags: tags, Value: 1},
		metrics.Sample{Metric: "http_req_failed", Tags: tags, Value: failed},
		metrics.Sample{Metric: "data_sent", Tags: tags, Value: float64(sentSize(res.Sent))},
	)
	for _, c := range res.Tests {
		pass := 0.0
		if c.Passed {
			pass = 1
		}
		samples = append(samples, metrics.Sample{Metric: "checks", Tags: metrics.Tags{Scenario: tags.Scenario, Request: tags.Request, Group: tags.Group}, Value: pass})
	}
	v.buf.AddMany(samples...)
}

// stream opens a WebSocket/Socket.IO session, sends the step's messages and
// holds the connection, counting messages and ack round trips.
func (v *vu) stream(ctx context.Context, st *Step, in engine.SendInput, tags metrics.Tags) error {
	start := time.Now()
	res := v.eng.Connect(ctx, in)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if res.Error != "" {
		v.t.errors.add(res.Error + " (" + tags.Request + ")")
		v.buf.Add(metrics.Sample{Metric: "ws_errors", Tags: tags, Value: 1})
		return nil
	}
	s := res.Session
	defer s.Close()
	v.buf.AddMany(
		metrics.Sample{Metric: "ws_sessions", Tags: tags, Value: 1},
		metrics.Sample{Metric: "ws_connecting", Tags: tags, Value: ms(time.Since(start))},
	)
	for _, m := range st.Send {
		out, _ := engine.ResolveOutgoing(protocol.Outgoing{Data: m.Content, Event: m.Event, Ack: m.Ack, Binary: m.Binary}, v.vars)
		if _, err := s.Send(ctx, out); err != nil {
			v.t.errors.add(err.Error() + " (" + tags.Request + ")")
			v.buf.Add(metrics.Sample{Metric: "ws_errors", Tags: tags, Value: 1})
			break
		}
		v.buf.Add(metrics.Sample{Metric: "ws_msgs_sent", Tags: tags, Value: 1})
	}
	hold := time.NewTimer(st.Hold.D())
	defer hold.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-hold.C:
			break loop
		case m, ok := <-s.Messages():
			if !ok {
				if err := s.Err(); err != nil {
					v.t.errors.add(err.Error() + " (" + tags.Request + ")")
					v.buf.Add(metrics.Sample{Metric: "ws_errors", Tags: tags, Value: 1})
				}
				break loop
			}
			switch {
			case m.Direction == "received" && m.Kind == "ack" && m.RTT > 0:
				v.buf.AddMany(
					metrics.Sample{Metric: "ws_msgs_received", Tags: tags, Value: 1},
					metrics.Sample{Metric: "sio_event_rtt", Tags: tags, Value: ms(m.RTT)},
				)
			case m.Direction == "received":
				v.buf.Add(metrics.Sample{Metric: "ws_msgs_received", Tags: tags, Value: 1})
			}
		}
	}
	v.buf.Add(metrics.Sample{Metric: "ws_session_duration", Tags: tags, Value: ms(time.Since(start))})
	return nil
}

func sentSize(r *model.Request) int {
	if r == nil {
		return 0
	}
	n := len(r.Method) + len(r.URL) + 12
	for _, h := range r.Headers {
		if !h.Disabled {
			n += len(h.Key) + len(h.Value) + 4
		}
	}
	if r.Body != nil {
		n += len(r.Body.Content)
	}
	return n
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func compileExpr(src string) (*vm.Program, error) {
	return expr.Compile(src, expr.AllowUndefinedVariables(), expr.AsBool())
}

func newVUVars(env *model.Environment, globals map[string]string) *vars.Set {
	v := engine.NewVars(env, nil)
	for k, val := range globals {
		v.Globals[k] = val
	}
	return v
}
