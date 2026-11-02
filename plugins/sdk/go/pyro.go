// Package pyro is the SDK for Pyro plugins written in Go.
//
// Build with the standard Go toolchain (1.24+):
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
//
// A plugin's main package registers its features in init() and has an empty
// main(). See plugins/examples for complete plugins.
package pyro

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ABIVersion is the plugin ABI this SDK implements.
const ABIVersion = 1

// KeyValue is a header or query parameter.
type KeyValue struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Request is what a protocol plugin executes (variables already resolved).
type Request struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers []KeyValue        `json:"headers,omitempty"`
	Query   []KeyValue        `json:"query,omitempty"`
	Body    string            `json:"body,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

// Option returns a request option, or def when it is unset.
func (r *Request) Option(key, def string) string {
	if v, ok := r.Options[key]; ok && v != "" {
		return v
	}
	return def
}

// Header returns the first enabled header with the name.
func (r *Request) Header(name string) string {
	for _, h := range r.Headers {
		if !h.Disabled && strings.EqualFold(h.Key, name) {
			return h.Value
		}
	}
	return ""
}

// SetHeader replaces (or adds) a header.
func (r *Request) SetHeader(name, value string) {
	for i, h := range r.Headers {
		if strings.EqualFold(h.Key, name) {
			r.Headers[i].Value, r.Headers[i].Disabled = value, false
			return
		}
	}
	r.Headers = append(r.Headers, KeyValue{Key: name, Value: value})
}

// Response is what a protocol plugin returns. Status is protocol-specific;
// use 0 when the protocol has no status code.
type Response struct {
	Status     int        `json:"status"`
	StatusText string     `json:"statusText,omitempty"`
	Headers    []KeyValue `json:"headers,omitempty"`
	Body       []byte     `json:"body,omitempty"`
	RemoteAddr string     `json:"remoteAddr,omitempty"`
}

// Context carries per-call settings.
type Context struct {
	// Config holds the plugin's settings as configured by the user.
	Config map[string]string
	// Params holds the auth type's or processor's fields.
	Params map[string]string
}

// Protocol adds a request type ("plugin:<id>").
type Protocol interface {
	Execute(ctx *Context, req *Request) (*Response, error)
}

// Auth adds an auth type ("plugin:<id>") that signs a request in place.
type Auth interface {
	Sign(ctx *Context, req *Request) error
}

// Response seen by a post-processor.
type ProcessResponse struct {
	Status     int        `json:"status"`
	StatusText string     `json:"statusText,omitempty"`
	Headers    []KeyValue `json:"headers,omitempty"`
	Body       []byte     `json:"body,omitempty"`
	TimeMs     float64    `json:"timeMs"`
}

// Process is one processor call. Before the request (Phase "pre") Request
// is unresolved and may be changed in place; after it (Phase "post")
// Request is what was sent and Response is set. Vars holds every variable
// in scope; Params holds the processor's fields.
type Process struct {
	Phase    string
	Request  *Request
	Response *ProcessResponse
	Vars     map[string]string
	set      map[string]string
	tests    []TestResult
}

// SetVar sets a variable in the run (later requests can use {{name}}).
func (p *Process) SetVar(name, value string) {
	if p.set == nil {
		p.set = map[string]string{}
	}
	p.set[name] = value
	if p.Vars != nil {
		p.Vars[name] = value
	}
}

// Test records a check, shown with the request's test results.
func (p *Process) Test(name string, passed bool, msg string) {
	p.tests = append(p.tests, TestResult{Name: name, Passed: passed, Error: msg})
}

// TestResult is a check reported by a processor.
type TestResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Error  string `json:"error,omitempty"`
}

// Processor adds a pre/post processor ("plugin:<id>") that runs around
// requests, like JMeter's pre and post processors.
type Processor interface {
	Process(ctx *Context, p *Process) error
}

// ProcessorFunc adapts a function to Processor.
type ProcessorFunc func(ctx *Context, p *Process) error

func (f ProcessorFunc) Process(ctx *Context, p *Process) error { return f(ctx, p) }

// ProtocolFunc adapts a function to Protocol.
type ProtocolFunc func(ctx *Context, req *Request) (*Response, error)

func (f ProtocolFunc) Execute(ctx *Context, req *Request) (*Response, error) { return f(ctx, req) }

// AuthFunc adapts a function to Auth.
type AuthFunc func(ctx *Context, req *Request) error

func (f AuthFunc) Sign(ctx *Context, req *Request) error { return f(ctx, req) }

var (
	protocols  = map[string]Protocol{}
	auths      = map[string]Auth{}
	processors = map[string]Processor{}
)

// RegisterProtocol registers a request type. Call it from init().
func RegisterProtocol(id string, p Protocol) { protocols[id] = p }

// RegisterAuth registers an auth type. Call it from init().
func RegisterAuth(id string, a Auth) { auths[id] = a }

// RegisterProcessor registers a pre/post processor. Call it from init().
func RegisterProcessor(id string, p Processor) { processors[id] = p }

// Log levels.
const (
	LevelDebug = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Logf writes to Pyro's log (shown next to the response).
func Logf(level int, format string, args ...any) {
	b := []byte(fmt.Sprintf(format, args...))
	logBytes(level, b)
}

// Conn is a network connection opened through the host. The host only
// allows addresses the plugin's manifest declares under capabilities.network.
type Conn struct {
	handle      int32
	readTimeout time.Duration
}

// ErrTimeout is returned by Read when no data arrived within the read timeout.
var ErrTimeout = errors.New("read timed out")

// Dial opens a TCP (or TLS) connection to host:port.
func Dial(addr string, useTLS bool, timeout time.Duration) (*Conn, error) {
	h, err := dial(addr, useTLS, timeout)
	if err != nil {
		return nil, err
	}
	return &Conn{handle: h, readTimeout: 10 * time.Second}, nil
}

// SetReadTimeout bounds each Read (default 10s).
func (c *Conn) SetReadTimeout(d time.Duration) { c.readTimeout = d }

func (c *Conn) Write(b []byte) (int, error) { return write(c.handle, b) }

// Read returns io.EOF when the peer closed the connection and ErrTimeout
// when nothing arrived in time.
func (c *Conn) Read(b []byte) (int, error) { return read(c.handle, b, c.readTimeout) }

func (c *Conn) Close() error {
	closeConn(c.handle)
	return nil
}

var _ io.ReadWriteCloser = (*Conn)(nil)

// --- message dispatch (shared by the wasm ABI and tests) -------------------

type message struct {
	Op       string            `json:"op"`
	ID       string            `json:"id,omitempty"`
	Request  *Request          `json:"request,omitempty"`
	Config   map[string]string `json:"config,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Phase    string            `json:"phase,omitempty"`
	Response *ProcessResponse  `json:"response,omitempty"`
	Vars     map[string]string `json:"vars,omitempty"`
}

type provided struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type reply struct {
	ABI      int               `json:"abi,omitempty"`
	Provides []provided        `json:"provides,omitempty"`
	Response *Response         `json:"response,omitempty"`
	Request  *Request          `json:"request,omitempty"`
	Vars     map[string]string `json:"vars,omitempty"`
	Tests    []TestResult      `json:"tests,omitempty"`
	Error    string            `json:"error,omitempty"`
}

func handle(m *message) reply {
	ctx := &Context{Config: m.Config, Params: m.Params}
	switch m.Op {
	case "describe":
		r := reply{ABI: ABIVersion}
		for id := range protocols {
			r.Provides = append(r.Provides, provided{"protocol", id})
		}
		for id := range auths {
			r.Provides = append(r.Provides, provided{"auth", id})
		}
		for id := range processors {
			r.Provides = append(r.Provides, provided{"processor", id})
		}
		return r
	case "process":
		pr, ok := processors[m.ID]
		if !ok || m.Request == nil {
			return reply{Error: fmt.Sprintf("no processor %q in this plugin", m.ID)}
		}
		p := &Process{Phase: m.Phase, Request: m.Request, Response: m.Response, Vars: m.Vars}
		if p.Vars == nil {
			p.Vars = map[string]string{}
		}
		if err := pr.Process(ctx, p); err != nil {
			return reply{Error: err.Error(), Vars: p.set, Tests: p.tests}
		}
		r := reply{Vars: p.set, Tests: p.tests}
		if m.Phase == "pre" {
			r.Request = p.Request
		}
		return r
	case "execute":
		p, ok := protocols[m.ID]
		if !ok || m.Request == nil {
			return reply{Error: fmt.Sprintf("no protocol %q in this plugin", m.ID)}
		}
		res, err := p.Execute(ctx, m.Request)
		if err != nil {
			return reply{Error: err.Error()}
		}
		if res == nil {
			res = &Response{}
		}
		return reply{Response: res}
	case "sign":
		a, ok := auths[m.ID]
		if !ok || m.Request == nil {
			return reply{Error: fmt.Sprintf("no auth type %q in this plugin", m.ID)}
		}
		if err := a.Sign(ctx, m.Request); err != nil {
			return reply{Error: err.Error()}
		}
		return reply{Request: m.Request}
	}
	return reply{Error: fmt.Sprintf("unknown operation %q", m.Op)}
}

// dispatch decodes a host message, runs it and encodes the reply. Panics in
// plugin code become error replies instead of crashing the instance.
func dispatch(in []byte) (out []byte) {
	defer func() {
		if r := recover(); r != nil {
			out, _ = json.Marshal(reply{Error: "plugin panicked: " + toString(r)})
		}
	}()
	var msg message
	if err := json.Unmarshal(in, &msg); err != nil {
		out, _ = json.Marshal(reply{Error: "invalid message: " + err.Error()})
		return out
	}
	out, _ = json.Marshal(handle(&msg))
	return out
}

func toString(v any) string { return fmt.Sprint(v) }
