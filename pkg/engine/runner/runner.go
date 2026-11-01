// Package runner executes a list of requests as a functional test run:
// iterations, data rows, setNextRequest and bail-out, with one shared set of
// variables so values captured early (a login token) feed later requests.
package runner

import (
	"context"
	"time"

	"github.com/pyrohq/pyro/pkg/engine"
	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/script"
	"github.com/pyrohq/pyro/pkg/engine/vars"
)

// Item is one request in run order.
type Item struct {
	Path    string
	Request *model.Request
	Chain   []*model.Collection
}

type Options struct {
	// Iterations defaults to the number of data rows, or 1.
	Iterations  int
	Data        []map[string]string
	Environment *model.Environment
	Globals     map[string]string
	// Delay is waited between requests.
	Delay time.Duration
	// Bail stops the run after the first failed request.
	Bail bool
	// OnResult is called after every request (progress reporting).
	OnResult func(ItemResult)
}

type ItemResult struct {
	Iteration int            `json:"iteration"`
	Path      string         `json:"path"`
	Name      string         `json:"name"`
	Kind      model.Kind     `json:"kind"`
	Result    *engine.Result `json:"result"`
	// Note explains a request that was not sent (e.g. a WebSocket item).
	Note string `json:"note,omitempty"`
}

func (r ItemResult) Passed() bool { return r.Note != "" || r.Result.Passed() }

type Summary struct {
	Iterations  int           `json:"iterations"`
	Requests    int           `json:"requests"`
	Failed      int           `json:"failed"`
	TestsPassed int           `json:"testsPassed"`
	TestsFailed int           `json:"testsFailed"`
	StartedAt   time.Time     `json:"startedAt"`
	Duration    time.Duration `json:"duration"`
	// Stopped explains an early end (bail, setNextRequest(null), cancel).
	Stopped string       `json:"stopped,omitempty"`
	Results []ItemResult `json:"results"`
}

func (s *Summary) OK() bool { return s.Failed == 0 && s.TestsFailed == 0 }

// Run executes items. Cancelling ctx stops after the in-flight request.
func Run(ctx context.Context, e *engine.Engine, items []Item, opts Options) *Summary {
	sum := &Summary{StartedAt: time.Now(), Results: []ItemResult{}}
	defer func() { sum.Duration = time.Since(sum.StartedAt) }()

	iterations := opts.Iterations
	if iterations <= 0 {
		iterations = max(len(opts.Data), 1)
	}

	var chain0 []*model.Collection
	if len(items) > 0 {
		chain0 = items[0].Chain
	}
	v := engine.NewVars(opts.Environment, chain0)
	for k, val := range opts.Globals {
		v.Globals[k] = val
	}
	rt, err := script.New()
	if err != nil {
		sum.Stopped = err.Error()
		return sum
	}

	byName := map[string]int{}
	for i := len(items) - 1; i >= 0; i-- {
		byName[items[i].Request.Name] = i
	}

	for it := 0; it < iterations; it++ {
		sum.Iterations++
		v.Data = map[string]string{}
		if len(opts.Data) > 0 {
			v.Data = opts.Data[it%len(opts.Data)]
		}
		for i := 0; i < len(items); {
			if ctx.Err() != nil {
				sum.Stopped = "cancelled"
				return sum
			}
			item := items[i]
			ir := runItem(ctx, e, rt, item, v, it)
			sum.add(ir)
			if opts.OnResult != nil {
				opts.OnResult(ir)
			}
			if opts.Bail && !ir.Passed() {
				sum.Stopped = "stopped after the first failure (--bail)"
				return sum
			}
			next := i + 1
			if r := ir.Result; r != nil && r.NextSet {
				if r.Next == nil {
					sum.Stopped = "stopped by setNextRequest(null) in " + item.Path
					return sum
				}
				j, ok := byName[*r.Next]
				if !ok {
					sum.Stopped = "setNextRequest: no request named " + *r.Next
					return sum
				}
				next = j
			}
			i = next
			if opts.Delay > 0 && i < len(items) {
				select {
				case <-time.After(opts.Delay):
				case <-ctx.Done():
				}
			}
		}
	}
	return sum
}

func runItem(ctx context.Context, e *engine.Engine, rt *script.Runtime, item Item, v *vars.Set, iteration int) ItemResult {
	ir := ItemResult{Iteration: iteration, Path: item.Path, Name: item.Request.Name, Kind: item.Request.Kind}
	if k := item.Request.Kind; k == model.KindWebSocket || k == model.KindSocketIO {
		ir.Note = string(k) + " requests are skipped in collection runs"
		return ir
	}
	v.Folders = v.Folders[:0]
	for i := len(item.Chain) - 1; i >= 1; i-- {
		if item.Chain[i] != nil && len(item.Chain[i].Variables) > 0 {
			v.Folders = append(v.Folders, vars.Map(item.Chain[i].Variables))
		}
	}
	ir.Result = e.Send(ctx, engine.SendInput{
		Request: item.Request, Path: item.Path, Chain: item.Chain, Vars: v, Iteration: iteration, Script: rt,
	})
	return ir
}

func (s *Summary) add(ir ItemResult) {
	s.Results = append(s.Results, ir)
	if ir.Note != "" {
		return
	}
	s.Requests++
	if !ir.Result.Passed() {
		s.Failed++
	}
	for _, t := range ir.Result.Tests {
		if t.Passed {
			s.TestsPassed++
		} else {
			s.TestsFailed++
		}
	}
}
