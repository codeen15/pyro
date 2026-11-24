package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// host owns one wazero runtime, shared by every plugin with the same memory
// limit (the runtime enforces the limit on each instance's memory).
type host struct {
	rt       wazero.Runtime
	cache    wazero.CompilationCache
	memoryMB int // the memory limit of each instance

	// js is the compile of the JS runtime, started on first use (js.go).
	jsMu   sync.Mutex
	js     *jsrtCompile
	closed bool
	// compileJSRT, when set (by tests, before the first compile), replaces
	// the JS runtime's compile.
	compileJSRT func(wazero.Runtime) (wazero.CompiledModule, error)
}

// testCacheDir, when set (by tests), replaces <dir>/cache so every test
// manager shares one compilation cache.
var testCacheDir string

func newHost(ctx context.Context, cacheDir string, memoryMB int) (*host, error) {
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(uint32(memoryMB * 16)) // 64 KiB pages
	var cache wazero.CompilationCache
	if cacheDir != "" {
		if c, err := wazero.NewCompilationCacheWithDir(cacheDir); err == nil {
			cache = c
			cfg = cfg.WithCompilationCache(c)
		}
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	if err := instantiateHostModule(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	return &host{rt: rt, cache: cache, memoryMB: memoryMB}, nil
}

// close closes the runtime, after a compile of the JS runtime in progress
// (which cannot be interrupted) finishes.
func (h *host) close(ctx context.Context) {
	h.jsMu.Lock()
	h.closed = true
	c := h.js
	h.jsMu.Unlock()
	if c != nil {
		<-c.done
	}
	h.rt.Close(ctx)
	if h.cache != nil {
		h.cache.Close(ctx)
	}
}

// callState is per-invocation host state, reached from host functions via
// the context: what the plugin may connect to, its open connections, logs.
type callState struct {
	caps    Capabilities
	plugin  string
	conns   map[int32]net.Conn
	next    int32
	lastErr string
	logs    []Log
}

type ctxKey struct{}

func stateOf(ctx context.Context) *callState {
	s, _ := ctx.Value(ctxKey{}).(*callState)
	return s
}

// Log is a line a plugin wrote.
type Log struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

const maxLogs = 200

var levels = []string{"debug", "info", "warn", "error"}

func readString(m api.Module, ptr, size uint32) (string, bool) {
	b, ok := m.Memory().Read(ptr, size)
	return string(b), ok
}

// instantiateHostModule exports the "pyro" import module.
func instantiateHostModule(ctx context.Context, rt wazero.Runtime) error {
	b := rt.NewHostModuleBuilder("pyro")
	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, level, ptr, size uint32) {
		s := stateOf(ctx)
		msg, ok := readString(m, ptr, size)
		if s == nil || !ok || len(s.logs) >= maxLogs {
			return
		}
		lv := "info"
		if int(level) < len(levels) {
			lv = levels[level]
		}
		s.logs = append(s.logs, Log{Level: lv, Message: msg})
	}).Export("log")

	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, ptr, size, useTLS, timeoutMS uint32) int32 {
		s := stateOf(ctx)
		addr, ok := readString(m, ptr, size)
		if s == nil || !ok {
			return -1
		}
		if !s.caps.Allows(addr) {
			s.lastErr = fmt.Sprintf("plugin %s may not connect to %s (not in its network capabilities)", s.plugin, addr)
			return -1
		}
		timeout := time.Duration(timeoutMS) * time.Millisecond
		if timeout <= 0 || timeout > time.Minute {
			timeout = 10 * time.Second
		}
		d := net.Dialer{Timeout: timeout}
		var conn net.Conn
		var err error
		if useTLS == 1 {
			host, _, _ := net.SplitHostPort(addr)
			conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: host})
		} else {
			conn, err = d.DialContext(ctx, "tcp", addr)
		}
		if err != nil {
			s.lastErr = fmt.Sprintf("could not connect to %s: %v", addr, err)
			return -1
		}
		s.next++
		s.conns[s.next] = conn
		return s.next
	}).Export("net_dial")

	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, h int32, ptr, size uint32) int32 {
		s := stateOf(ctx)
		data, ok := m.Memory().Read(ptr, size)
		if s == nil || !ok || s.conns[h] == nil {
			return -1
		}
		n, err := s.conns[h].Write(data)
		if err != nil {
			s.lastErr = err.Error()
			return -1
		}
		return int32(n)
	}).Export("net_write")

	// net_read: bytes read, 0 at EOF, -2 on timeout, -1 on error.
	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, h int32, ptr, size, timeoutMS uint32) int32 {
		s := stateOf(ctx)
		if s == nil || s.conns[h] == nil {
			return -1
		}
		conn := s.conns[h]
		deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		_ = conn.SetReadDeadline(deadline)
		buf := make([]byte, size)
		n, err := conn.Read(buf)
		if n > 0 {
			if !m.Memory().Write(ptr, buf[:n]) {
				return -1
			}
			return int32(n)
		}
		var ne net.Error
		switch {
		case err == nil:
			return -2
		case errors.As(err, &ne) && ne.Timeout():
			return -2
		case errors.Is(err, os.ErrDeadlineExceeded):
			return -2
		case errors.Is(err, io.EOF):
			return 0
		}
		s.lastErr = err.Error()
		return -1
	}).Export("net_read")

	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, h int32) {
		if s := stateOf(ctx); s != nil && s.conns[h] != nil {
			s.conns[h].Close()
			delete(s.conns, h)
		}
	}).Export("net_close")

	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, ptr, size uint32) uint32 {
		s := stateOf(ctx)
		if s == nil || s.lastErr == "" {
			return 0
		}
		msg := []byte(s.lastErr)
		if uint32(len(msg)) > size {
			msg = msg[:size]
		}
		m.Memory().Write(ptr, msg)
		return uint32(len(msg))
	}).Export("last_error")

	_, err := b.Instantiate(ctx)
	return err
}

// instance is one instantiated module; not safe for concurrent use.
type instance struct {
	mod    api.Module
	alloc  api.Function
	free   api.Function
	invoke api.Function
	stderr *stderrWatch
}

// stderrWatch passes an instance's stderr through to the host's and notes
// whether the plugin reported running out of memory (as the Go runtime
// does before it traps).
type stderrWatch struct {
	mu   sync.Mutex
	tail []byte // the last bytes written, so a message split across writes is seen
	oom  bool
}

func (w *stderrWatch) Write(b []byte) (int, error) {
	w.mu.Lock()
	w.tail = append(w.tail, b...)
	if bytes.Contains(w.tail, []byte("out of memory")) {
		w.oom = true
	}
	if n := len(w.tail); n > 64 {
		w.tail = append(w.tail[:0], w.tail[n-64:]...)
	}
	w.mu.Unlock()
	return os.Stderr.Write(b)
}

func (w *stderrWatch) sawOOM() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.oom
}

// outOfMemory reports whether a trapped instance most likely ran out of
// memory: it said so on stderr, or its memory is within 10% of the limit
// (a runtime that traps without saying why).
func (in *instance) outOfMemory(limitMB int) bool {
	if in.stderr != nil && in.stderr.sawOOM() {
		return true
	}
	limit := uint64(limitMB) << 20
	return limit > 0 && uint64(in.mod.Memory().Size())*10 >= limit*9
}

// failed turns a failed send's error into the one the caller sees: a
// crash of an instance that ran out of memory says so.
func (p *pool) failed(in *instance, err error) error {
	var c *crashError
	if errors.As(err, &c) && in.outOfMemory(p.h.memoryMB) {
		return fmt.Errorf("plugin %s ran out of memory (limit %d MB; raise limits.memoryMB)", p.name, p.h.memoryMB)
	}
	return err
}

// pool keeps idle instances of one compiled module and caps how many are
// live at once.
type pool struct {
	h    *host
	cm   wazero.CompiledModule
	name string // plugin name, for errors
	// max is the most live instances (limits.maxInstances); 0 is unbounded.
	// slots holds one token per live instance; nil when max is 0.
	max   int
	slots chan struct{}
	mu    sync.Mutex
	idle  []*instance
	// live counts instances handed out by get and not yet returned.
	live int
	// retired: the plugin was removed or replaced. Returned instances are
	// closed, and the compiled module once the last one is back.
	retired  bool
	cmClosed bool
	// init, if set, is sent to each new instance before anything else (the
	// JS runtime's load); an instance whose init fails is closed.
	init *loadMsg
	// sharedCM: cm belongs to the host (the JS runtime), not this pool, so
	// retiring the pool leaves it open.
	sharedCM bool
}

const maxIdle = 4096

func newPool(h *host, cm wazero.CompiledModule, name string, max int) *pool {
	p := &pool{h: h, cm: cm, name: name, max: max}
	if max > 0 {
		p.slots = make(chan struct{}, max)
	}
	return p
}

// acquire takes a slot for one live instance, waiting until one is free or
// ctx ends (then the plugin is busy).
func (p *pool) acquire(ctx context.Context) error {
	if p.slots == nil {
		return nil
	}
	select {
	case p.slots <- struct{}{}:
		return nil
	default:
	}
	select {
	case p.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return &busyError{name: p.name, max: p.max}
	}
}

// busyError is ErrBusy with the plugin's name and instance cap.
type busyError struct {
	name string
	max  int
}

func (e *busyError) Error() string {
	return fmt.Sprintf("plugin %s is busy: all %d instances in use", e.name, e.max)
}

func (e *busyError) Unwrap() error { return ErrBusy }

func (p *pool) release() {
	if p.slots != nil {
		<-p.slots
	}
}

// get returns an instance holding a slot; put or discard releases it.
// A new instance is sent p.init first; ctx carries the call's state.
func (p *pool) get(ctx context.Context) (*instance, error) {
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		in := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.live++
		p.mu.Unlock()
		return in, nil
	}
	if p.cmClosed {
		p.mu.Unlock()
		p.release()
		return nil, reloaded(p.name)
	}
	// Counting the instance now keeps the compiled module open while it
	// is instantiated, even if the pool is retired meanwhile.
	p.live++
	p.mu.Unlock()
	// Instantiation must outlive the caller's request context, or wazero
	// would close the module when that context ends. Plugins see the real
	// wall and monotonic clocks and cryptographic random numbers (WASI's
	// defaults are a fixed 2022 clock and the same random sequence in every
	// instance), but sleeping stays a no-op: a real sleep would run past
	// the call's deadline, which only cuts in when wasm code runs.
	stderr := &stderrWatch{}
	mod, err := p.h.rt.InstantiateModule(context.WithoutCancel(ctx), p.cm,
		wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize").WithStderr(stderr).
			WithSysWalltime().WithSysNanotime().WithRandSource(rand.Reader))
	if err != nil {
		p.done(nil)
		return nil, fmt.Errorf("starting plugin: %w", err)
	}
	in := &instance{mod: mod, alloc: mod.ExportedFunction("pyro_alloc"), free: mod.ExportedFunction("pyro_free"), invoke: mod.ExportedFunction("pyro_invoke"), stderr: stderr}
	if in.alloc == nil || in.free == nil || in.invoke == nil {
		mod.Close(ctx)
		p.done(nil)
		return nil, errors.New("not a Pyro plugin: missing pyro_alloc, pyro_free or pyro_invoke exports")
	}
	if p.init != nil {
		// A failed load may leave part of the script registered, so the
		// instance is never used again.
		var r loadReply
		if err := in.send(ctx, p.init, &r); err != nil {
			err = p.failed(in, err)
			p.discard(in)
			return nil, err
		}
		if err := r.err(p.name); err != nil {
			p.discard(in)
			return nil, err
		}
	}
	return in, nil
}

// put returns a healthy instance to the idle list and releases its slot.
func (p *pool) put(in *instance) {
	defer p.release()
	limit := maxIdle
	if p.max > 0 {
		limit = min(p.max, maxIdle)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.live--
	if !p.retired && len(p.idle) < limit {
		p.idle = append(p.idle, in)
		return
	}
	in.mod.Close(context.Background())
	p.closeModuleIfDrained()
}

// discard closes an instance that may be broken and releases its slot.
func (p *pool) discard(in *instance) {
	p.done(in)
}

// done closes in (if any), stops counting it and releases its slot.
func (p *pool) done(in *instance) {
	defer p.release()
	if in != nil {
		in.mod.Close(context.Background())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.live--
	p.closeModuleIfDrained()
}

// retire closes the idle instances now; calls in flight finish on their
// instances, which put closes as they come back, and the compiled module
// is closed with the last of them. Safe to call more than once.
func (p *pool) retire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retired = true
	for _, in := range p.idle {
		in.mod.Close(context.Background())
	}
	p.idle = nil
	p.closeModuleIfDrained()
}

// closeModuleIfDrained closes a retired pool's compiled module once no
// instance is out. The caller holds p.mu.
func (p *pool) closeModuleIfDrained() {
	if p.retired && p.live == 0 && !p.cmClosed {
		p.cmClosed = true
		if !p.sharedCM {
			p.cm.Close(context.Background())
		}
	}
}

// call sends one JSON message and decodes the reply into out. A trap or
// timeout discards the instance (its memory may be inconsistent).
func (p *pool) call(ctx context.Context, st *callState, msg any, out any) error {
	ctx = context.WithValue(ctx, ctxKey{}, st)
	defer func() {
		for _, c := range st.conns {
			c.Close()
		}
	}()
	in, err := p.get(ctx)
	if err != nil {
		return err
	}
	if err := in.send(ctx, msg, out); err != nil {
		err = p.failed(in, err) // before discard closes the instance
		p.discard(in)
		return err
	}
	p.put(in)
	return nil
}

// send writes one JSON message to the instance and decodes the reply into
// out. After an error the instance must be discarded.
func (in *instance) send(ctx context.Context, msg any, out any) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	res, err := in.alloc.Call(ctx, uint64(len(payload)))
	if err != nil {
		return trap(err)
	}
	ptr := uint32(res[0])
	if !in.mod.Memory().Write(ptr, payload) {
		return errors.New("plugin returned an invalid buffer")
	}
	res, err = in.invoke.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return trap(err)
	}
	rp, rl := uint32(res[0]>>32), uint32(res[0])
	reply, okRead := in.mod.Memory().Read(rp, rl)
	if !okRead {
		return errors.New("plugin returned an invalid reply buffer")
	}
	err = json.Unmarshal(reply, out)
	_, _ = in.free.Call(ctx, uint64(rp), uint64(rl))
	if err != nil {
		return fmt.Errorf("plugin reply is not valid JSON: %w", err)
	}
	return nil
}

func trap(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("plugin timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("cancelled")
	}
	return &crashError{err: err}
}

// crashError is a trap in plugin code (not a timeout or cancellation).
type crashError struct{ err error }

func (e *crashError) Error() string { return fmt.Sprintf("plugin crashed: %v", e.err) }
func (e *crashError) Unwrap() error { return e.err }
