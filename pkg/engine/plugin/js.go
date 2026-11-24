package plugin

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
)

// JavaScript plugins (runtime: js) run on jsrt, a Go plugin that embeds the
// goja interpreter (plugins/runtime/js). Every JS plugin shares one compiled
// jsrt per memory limit; each plugin has its own instance pool, and each new
// instance is sent the plugin's script in a "load" message before any other.

//go:embed jsrt.wasm.gz
var jsrtGz []byte

// MaxScriptSize bounds a JS plugin's script.
const MaxScriptSize = 4 << 20

// jsrtWasm unpacks the runtime (~21 MB). It runs once per compile (one per
// distinct memory limit), so the unpacked bytes aren't kept around.
func jsrtWasm() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(jsrtGz))
	if err != nil {
		return nil, fmt.Errorf("JavaScript runtime: %w", err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("JavaScript runtime: %w", err)
	}
	return b, nil
}

// jsrtCompile is one host's compile of jsrt; done closes when it finishes.
type jsrtCompile struct {
	done chan struct{}
	cm   wazero.CompiledModule
	err  error
}

// jsrt returns the host's compiled JS runtime, compiling it on first use.
// Callers that arrive during the compile wait for it (or for ctx).
func (h *host) jsrt(ctx context.Context) (wazero.CompiledModule, error) {
	h.jsMu.Lock()
	if h.closed {
		h.jsMu.Unlock()
		return nil, errors.New("plugins are shut down")
	}
	c := h.js
	if c == nil {
		c = &jsrtCompile{done: make(chan struct{})}
		h.js = c
		go func() {
			defer close(c.done)
			wasm, err := jsrtWasm()
			if err != nil {
				c.err = err
				return
			}
			// Not tied to any caller: the compile is shared.
			c.cm, c.err = h.rt.CompileModule(context.Background(), wasm)
			if c.err != nil {
				c.err = fmt.Errorf("compiling the JavaScript runtime: %w", c.err)
			}
		}()
	}
	h.jsMu.Unlock()
	select {
	case <-c.done:
		return c.cm, c.err
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for the JavaScript runtime to compile: %w", ctx.Err())
	}
}

// compileJS returns the host for memoryMB and its compiled JS runtime.
func (m *Manager) compileJS(ctx context.Context, memoryMB int) (*host, wazero.CompiledModule, error) {
	h, err := m.host(ctx, memoryMB)
	if err != nil {
		return nil, nil, err
	}
	cm, err := h.jsrt(ctx)
	if err != nil {
		return nil, nil, err
	}
	return h, cm, nil
}

// Precompile starts compiling the JS runtime in the background for each
// ready JS plugin's memory limit, so its first request doesn't wait for the
// compile, and has Reload do the same for JS plugins it finds later. It
// doesn't block. Only long-lived processes (serve, agent) should call it:
// Close waits for a compile in progress, which would hold up a short
// command, and a short command compiles on demand instead.
func (m *Manager) Precompile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.precompile = true
	m.precompileReady(m.byNm)
}

// precompileReady precompiles the runtime for each ready JS plugin in byNm
// when Precompile mode is on. The caller holds m.mu.
func (m *Manager) precompileReady(byNm map[string]*loaded) {
	if !m.precompile || m.closed {
		return
	}
	seen := map[int]bool{}
	for _, l := range byNm {
		mb := l.man.Limits.MemoryMB
		if l.man.IsJS() && m.info(l).Ready && !seen[mb] {
			seen[mb] = true
			go func() { _, _, _ = m.compileJS(context.Background(), mb) }()
		}
	}
}

// checkScript applies the install rules for a JS plugin's script.
func checkScript(name string, src []byte) error {
	if len(src) > MaxScriptSize {
		return fmt.Errorf("%s is larger than 4 MB", name)
	}
	if !utf8.Valid(src) {
		return fmt.Errorf("%s is not valid UTF-8", name)
	}
	return nil
}

// loadMsg is the jsrt "load" op, sent to each new instance before any other.
type loadMsg struct {
	Op    string            `json:"op"`
	Entry string            `json:"entry"`
	Files map[string]string `json:"files"`
}

func newLoadMsg(man *Manifest, src []byte) *loadMsg {
	return &loadMsg{Op: "load", Entry: man.Module, Files: map[string]string{man.Module: string(src)}}
}

// jsPool makes a pool for a JS plugin on the shared runtime: its instances
// load src first, and retiring it leaves the shared compiled module open.
func jsPool(h *host, cm wazero.CompiledModule, man *Manifest, src []byte, max int) *pool {
	p := newPool(h, cm, man.Name, max)
	p.init = newLoadMsg(man, src)
	p.sharedCM = true
	return p
}

// readScript reads an installed JS plugin's script and checks it.
func readScript(l *loaded) ([]byte, error) {
	src, err := os.ReadFile(filepath.Join(l.dir, l.man.Module))
	if err != nil {
		return nil, err
	}
	return src, checkScript(l.man.Module, src)
}

// loadReply is jsrt's answer to load.
type loadReply struct {
	Error string `json:"error"`
}

func (r loadReply) err(plugin string) error {
	if r.Error == "" {
		return nil
	}
	return fmt.Errorf("plugin %s failed to load: %s", plugin, r.Error)
}
