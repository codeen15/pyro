// Package check evaluates declarative assertions and extractors against a
// response. Both are pure Go so they stay cheap on the load-test hot path.
package check

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
)

// Result is one assertion or script test outcome.
type Result struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Error  string `json:"error,omitempty"`
	// Source is "assertion" or "script".
	Source string `json:"source"`
}

// Response wraps a protocol response with a lazily parsed JSON body, shared by
// assertions, extractors and scripts.
type Response struct {
	*protocol.Response
	parsed  any
	jsonErr error
	done    bool
}

func Wrap(r *protocol.Response) *Response { return &Response{Response: r} }

// JSON returns the body parsed as JSON.
func (r *Response) JSON() (any, error) {
	if !r.done {
		r.done = true
		r.parsed, r.jsonErr = oj.Parse(r.Body)
		if r.jsonErr != nil {
			r.jsonErr = fmt.Errorf("body is not JSON")
		}
	}
	return r.parsed, r.jsonErr
}

// Header returns the first value of a header, case-insensitively.
func (r *Response) Header(name string) (string, bool) {
	for _, h := range r.Headers {
		if strings.EqualFold(h.Key, name) {
			return h.Value, true
		}
	}
	return "", false
}

// Assert evaluates every assertion. A nil response fails them all.
func Assert(as []model.Assertion, r *Response) []Result {
	out := make([]Result, 0, len(as))
	for _, a := range as {
		res := Result{Name: Describe(a), Source: "assertion"}
		if r == nil || r.Response == nil {
			res.Error = "no response"
		} else if err := assertOne(a, r); err != nil {
			res.Error = err.Error()
		} else {
			res.Passed = true
		}
		out = append(out, res)
	}
	return out
}

// Describe gives an assertion a readable name, e.g. "status eq 201".
func Describe(a model.Assertion) string {
	op := opOf(a)
	parts := []string{a.Type}
	if a.Path != "" {
		parts = append(parts, a.Path)
	}
	parts = append(parts, op)
	if op != "exists" && op != "notexists" {
		parts = append(parts, a.Value)
	}
	return strings.Join(parts, " ")
}

func opOf(a model.Assertion) string {
	if a.Op != "" {
		return a.Op
	}
	if a.Value == "" && (a.Type == "jsonpath" || a.Type == "header") {
		return "exists"
	}
	return "eq"
}

func assertOne(a model.Assertion, r *Response) error {
	op := opOf(a)
	var actual string
	var present bool
	switch a.Type {
	case "status":
		actual, present = strconv.Itoa(r.Status), true
	case "duration":
		actual, present = strconv.FormatFloat(float64(r.Timings.Total.Microseconds())/1000, 'f', -1, 64), true
	case "header":
		actual, present = r.Header(a.Path)
	case "body":
		actual, present = string(r.Body), true
	case "jsonpath":
		v, ok, err := jsonPath(r, a.Path)
		if err != nil {
			return err
		}
		actual, present = v, ok
	default:
		return fmt.Errorf("unknown assertion type %q", a.Type)
	}
	return compare(op, actual, present, a.Value)
}

func compare(op, actual string, present bool, want string) error {
	switch op {
	case "exists":
		if !present {
			return fmt.Errorf("not present")
		}
		return nil
	case "notexists":
		if present {
			return fmt.Errorf("present (%s)", short(actual))
		}
		return nil
	}
	if !present {
		return fmt.Errorf("not present")
	}
	switch op {
	case "eq":
		if actual != want {
			return fmt.Errorf("got %s, want %s", short(actual), want)
		}
	case "neq":
		if actual == want {
			return fmt.Errorf("got %s", short(actual))
		}
	case "contains":
		if !strings.Contains(actual, want) {
			return fmt.Errorf("%s does not contain %q", short(actual), want)
		}
	case "matches":
		re, err := regexp.Compile(want)
		if err != nil {
			return fmt.Errorf("invalid pattern: %v", err)
		}
		if !re.MatchString(actual) {
			return fmt.Errorf("%s does not match %s", short(actual), want)
		}
	case "lt", "lte", "gt", "gte":
		a, err1 := strconv.ParseFloat(actual, 64)
		w, err2 := strconv.ParseFloat(want, 64)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("cannot compare %s and %s as numbers", short(actual), want)
		}
		ok := map[string]bool{"lt": a < w, "lte": a <= w, "gt": a > w, "gte": a >= w}[op]
		if !ok {
			return fmt.Errorf("got %s, want %s %s", short(actual), op, want)
		}
	default:
		return fmt.Errorf("unknown operator %q", op)
	}
	return nil
}

// jsonPath returns the first match rendered as a string: strings as-is,
// anything else as compact JSON.
func jsonPath(r *Response, path string) (string, bool, error) {
	data, err := r.JSON()
	if err != nil {
		return "", false, err
	}
	x, err := jp.ParseString(path)
	if err != nil {
		return "", false, fmt.Errorf("invalid JSONPath %q: %v", path, err)
	}
	got := x.Get(data)
	if len(got) == 0 {
		return "", false, nil
	}
	return render(got[0]), true, nil
}

func render(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "null"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func short(s string) string {
	if len(s) > 80 {
		return strconv.Quote(s[:77] + "...")
	}
	return strconv.Quote(s)
}

// Extract evaluates extractors and returns the captured variables plus one
// problem per extractor that found nothing.
func Extract(ex []model.Extractor, r *Response) (map[string]string, []string) {
	vars := map[string]string{}
	var problems []string
	if r == nil || r.Response == nil {
		return vars, nil
	}
	for _, e := range ex {
		v, ok, err := extractOne(e, r)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", e.Var, err))
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: nothing matched %s %s", e.Var, e.From, e.Path))
		default:
			vars[e.Var] = v
		}
	}
	return vars, problems
}

func extractOne(e model.Extractor, r *Response) (string, bool, error) {
	switch e.From {
	case "jsonpath":
		return jsonPath(r, e.Path)
	case "header":
		v, ok := r.Header(e.Path)
		return v, ok, nil
	case "status":
		return strconv.Itoa(r.Status), true, nil
	case "regex":
		re, err := regexp.Compile(e.Path)
		if err != nil {
			return "", false, fmt.Errorf("invalid pattern: %v", err)
		}
		m := re.FindSubmatch(r.Body)
		if m == nil {
			return "", false, nil
		}
		if len(m) > 1 {
			return string(m[1]), true, nil
		}
		return string(m[0]), true, nil
	}
	return "", false, fmt.Errorf("unknown source %q", e.From)
}
