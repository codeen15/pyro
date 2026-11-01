package plugin

import (
	"context"
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

// host owns the wazero runtime shared by all plugins.
type host struct {
	rt    wazero.Runtime
	cache wazero.CompilationCache
}

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
	return &host{rt: rt, cache: cache}, nil
}

func (h *host) close(ctx context.Context) {
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
}

// pool keeps idle instances of one compiled module.
type pool struct {
	h    *host
	cm   wazero.CompiledModule
	mu   sync.Mutex
	idle []*instance
}

const maxIdle = 4096

func (p *pool) get(ctx context.Context) (*instance, error) {
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		in := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		return in, nil
	}
	p.mu.Unlock()
	// Instantiation must outlive the caller's request context, or wazero
	// would close the module when that context ends.
	mod, err := p.h.rt.InstantiateModule(context.WithoutCancel(ctx), p.cm,
		wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize").WithStderr(os.Stderr))
	if err != nil {
		return nil, fmt.Errorf("starting plugin: %w", err)
	}
	in := &instance{mod: mod, alloc: mod.ExportedFunction("pyro_alloc"), free: mod.ExportedFunction("pyro_free"), invoke: mod.ExportedFunction("pyro_invoke")}
	if in.alloc == nil || in.free == nil || in.invoke == nil {
		mod.Close(ctx)
		return nil, errors.New("not a Pyro plugin: missing pyro_alloc, pyro_free or pyro_invoke exports")
	}
	return in, nil
}

func (p *pool) put(in *instance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) < maxIdle {
		p.idle = append(p.idle, in)
		return
	}
	in.mod.Close(context.Background())
}

func (p *pool) close(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, in := range p.idle {
		in.mod.Close(ctx)
	}
	p.idle = nil
}

// call sends one JSON message and decodes the reply into out. A trap or
// timeout discards the instance (its memory may be inconsistent).
func (p *pool) call(ctx context.Context, st *callState, msg any, out any) error {
	in, err := p.get(ctx)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		for _, c := range st.conns {
			c.Close()
		}
		if ok {
			p.put(in)
		} else {
			in.mod.Close(context.Background())
		}
	}()
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, ctxKey{}, st)
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
	ok = true
	return nil
}

func trap(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("plugin timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("cancelled")
	}
	return fmt.Errorf("plugin crashed: %v", err)
}
