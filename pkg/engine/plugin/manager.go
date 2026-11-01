package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pyrohq/pyro/pkg/engine/model"
	"github.com/pyrohq/pyro/pkg/engine/protocol"
)

var (
	ErrNotFound    = errors.New("plugin not found")
	ErrNotApproved = errors.New("plugin needs approval")
	ErrDisabled    = errors.New("plugin is disabled")
)

// DefaultTimeout bounds one plugin call.
const DefaultTimeout = 60 * time.Second

// state is persisted in <dir>/state.json.
type state struct {
	Plugins map[string]*pluginState `json:"plugins"`
}

type pluginState struct {
	Enabled bool `json:"enabled"`
	// Approved is the capability set the user approved; a plugin whose
	// manifest asks for anything different needs approval again.
	Approved *Capabilities     `json:"approved,omitempty"`
	Config   map[string]string `json:"config,omitempty"`
}

// Info describes an installed plugin.
type Info struct {
	Manifest
	Enabled  bool              `json:"enabled"`
	Approved bool              `json:"approved"`
	Config   map[string]string `json:"config,omitempty"`
	Ready    bool              `json:"ready"`
	Error    string            `json:"error,omitempty"`
}

type loaded struct {
	man  *Manifest
	dir  string
	pool *pool
	err  error
}

// Manager owns installed plugins under one directory (~/.pyro/plugins).
// Safe for concurrent use.
type Manager struct {
	dir  string
	mu   sync.Mutex
	h    *host
	st   state
	byNm map[string]*loaded
}

// Open loads installed plugins from dir; modules compile on first use.
func Open(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, byNm: map[string]*loaded{}, st: state{Plugins: map[string]*pluginState{}}}
	if data, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		if err := json.Unmarshal(data, &m.st); err != nil {
			return nil, fmt.Errorf("plugin state: %w", err)
		}
		if m.st.Plugins == nil {
			m.st.Plugins = map[string]*pluginState{}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "cache" {
			continue
		}
		l := &loaded{dir: filepath.Join(dir, e.Name())}
		data, err := os.ReadFile(filepath.Join(l.dir, ManifestFile))
		if err == nil {
			l.man, err = ParseManifest(data)
		}
		if err != nil {
			l.err = err
			l.man = &Manifest{Name: e.Name()}
		}
		m.byNm[l.man.Name] = l
	}
	return m, nil
}

func (m *Manager) Dir() string { return m.dir }

func (m *Manager) saveState() error {
	data, _ := json.MarshalIndent(m.st, "", "  ")
	tmp := filepath.Join(m.dir, ".state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(m.dir, "state.json"))
}

func (m *Manager) info(l *loaded) Info {
	ps := m.st.Plugins[l.man.Name]
	in := Info{Manifest: *l.man}
	if ps != nil {
		in.Enabled = ps.Enabled
		in.Approved = ps.Approved != nil && ps.Approved.Equal(l.man.Capabilities)
		in.Config = ps.Config
	}
	if l.err != nil {
		in.Error = l.err.Error()
	}
	in.Ready = in.Enabled && in.Approved && l.err == nil
	return in
}

// List returns installed plugins sorted by name.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.byNm))
	for _, l := range m.byNm {
		out = append(out, m.info(l))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one plugin's info.
func (m *Manager) Get(name string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.byNm[name]
	if l == nil {
		return Info{}, ErrNotFound
	}
	return m.info(l), nil
}

// InstallDir installs (or upgrades) a plugin from a directory holding its
// manifest and module. It is enabled but not usable until approved, unless
// approve is true or the capabilities are unchanged from an earlier approval.
func (m *Manager) InstallDir(src string, approve bool) (Info, error) {
	data, err := os.ReadFile(filepath.Join(src, ManifestFile))
	if err != nil {
		return Info{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	man, err := ParseManifest(data)
	if err != nil {
		return Info{}, err
	}
	module, err := os.ReadFile(filepath.Join(src, man.Module))
	if err != nil {
		return Info{}, fmt.Errorf("module %s: %w", man.Module, err)
	}
	return m.install(man, data, module, approve)
}

// InstallArchive installs a plugin from a zip archive (.zip or
// .pyro-plugin) containing pyro-plugin.yaml and the module.
func (m *Manager) InstallArchive(archive []byte, approve bool) (Info, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return Info{}, fmt.Errorf("not a plugin archive (zip): %w", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		name := filepath.Base(f.Name) // archives may wrap files in a folder
		if f.FileInfo().IsDir() || (name != ManifestFile && !strings.HasSuffix(name, ".wasm")) {
			continue
		}
		if f.UncompressedSize64 > 256<<20 {
			return Info{}, fmt.Errorf("%s is larger than 256 MB", name)
		}
		rc, err := f.Open()
		if err != nil {
			return Info{}, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 256<<20))
		rc.Close()
		if err != nil {
			return Info{}, err
		}
		files[name] = b
	}
	data, ok := files[ManifestFile]
	if !ok {
		return Info{}, fmt.Errorf("archive has no %s", ManifestFile)
	}
	man, err := ParseManifest(data)
	if err != nil {
		return Info{}, err
	}
	module, ok := files[man.Module]
	if !ok {
		return Info{}, fmt.Errorf("archive has no %s", man.Module)
	}
	return m.install(man, data, module, approve)
}

func (m *Manager) install(man *Manifest, manifest, module []byte, approve bool) (Info, error) {
	if !bytes.HasPrefix(module, []byte("\x00asm")) {
		return Info{}, fmt.Errorf("%s is not a WebAssembly module", man.Module)
	}
	// Check the module actually provides what the manifest declares.
	if err := m.verify(man, module); err != nil {
		return Info{}, err
	}
	dest := filepath.Join(m.dir, man.Name)
	tmp, err := os.MkdirTemp(m.dir, ".install-*")
	if err != nil {
		return Info{}, err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, ManifestFile), manifest, 0o644); err != nil {
		return Info{}, err
	}
	if err := os.WriteFile(filepath.Join(tmp, man.Module), module, 0o644); err != nil {
		return Info{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if old := m.byNm[man.Name]; old != nil && old.pool != nil {
		old.pool.close(context.Background())
	}
	if err := os.RemoveAll(dest); err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return Info{}, err
	}
	ps := m.st.Plugins[man.Name]
	if ps == nil {
		ps = &pluginState{Enabled: true}
		m.st.Plugins[man.Name] = ps
	}
	if approve {
		caps := man.Capabilities
		ps.Approved = &caps
	}
	l := &loaded{man: man, dir: dest}
	m.byNm[man.Name] = l
	return m.info(l), m.saveState()
}

// verify instantiates the module once and checks describe() against the manifest.
func (m *Manager) verify(man *Manifest, module []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h, err := m.host(ctx)
	if err != nil {
		return err
	}
	cm, err := h.rt.CompileModule(ctx, module)
	if err != nil {
		return fmt.Errorf("compiling %s: %w", man.Module, err)
	}
	p := &pool{h: h, cm: cm}
	defer p.close(ctx)
	var r struct {
		ABI      int `json:"abi"`
		Provides []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"provides"`
		Error string `json:"error"`
	}
	if err := p.call(ctx, &callState{plugin: man.Name, conns: map[int32]net.Conn{}}, map[string]string{"op": "describe"}, &r); err != nil {
		return err
	}
	if r.Error != "" {
		return fmt.Errorf("describe: %s", r.Error)
	}
	if r.ABI != ABIVersion {
		return fmt.Errorf("module speaks ABI %d, this Pyro speaks %d", r.ABI, ABIVersion)
	}
	have := map[string]bool{}
	for _, p := range r.Provides {
		have[p.Kind+":"+p.ID] = true
	}
	for _, p := range man.Provides {
		if !have[p.Kind+":"+p.ID] {
			return fmt.Errorf("the manifest declares %s %q but the module doesn't register it", p.Kind, p.ID)
		}
	}
	return nil
}

func (m *Manager) host(ctx context.Context) (*host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.h == nil {
		h, err := newHost(ctx, filepath.Join(m.dir, "cache"), 512)
		if err != nil {
			return nil, err
		}
		m.h = h
	}
	return m.h, nil
}

// Remove uninstalls a plugin.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.byNm[name]
	if l == nil {
		return ErrNotFound
	}
	if l.pool != nil {
		l.pool.close(context.Background())
	}
	if err := os.RemoveAll(l.dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	delete(m.byNm, name)
	delete(m.st.Plugins, name)
	return m.saveState()
}

// Approve records the plugin's current capabilities as approved.
func (m *Manager) Approve(name string) (Info, error) {
	return m.update(name, func(l *loaded, ps *pluginState) {
		caps := l.man.Capabilities
		ps.Approved = &caps
	})
}

func (m *Manager) SetEnabled(name string, enabled bool) (Info, error) {
	return m.update(name, func(_ *loaded, ps *pluginState) { ps.Enabled = enabled })
}

func (m *Manager) SetConfig(name string, cfg map[string]string) (Info, error) {
	return m.update(name, func(_ *loaded, ps *pluginState) { ps.Config = cfg })
}

func (m *Manager) update(name string, f func(*loaded, *pluginState)) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.byNm[name]
	if l == nil {
		return Info{}, ErrNotFound
	}
	ps := m.st.Plugins[name]
	if ps == nil {
		ps = &pluginState{Enabled: true}
		m.st.Plugins[name] = ps
	}
	f(l, ps)
	return m.info(l), m.saveState()
}

// find returns the ready plugin providing kind/id, compiling it on first use.
func (m *Manager) find(ctx context.Context, kind, id string) (*loaded, *pluginState, error) {
	m.mu.Lock()
	var hit *loaded
	for _, l := range m.byNm {
		for _, p := range l.man.Provides {
			if p.Kind == kind && p.ID == id {
				hit = l
			}
		}
	}
	if hit == nil {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("no installed plugin provides %s %q: %w", kind, id, ErrNotFound)
	}
	info := m.info(hit)
	ps := m.st.Plugins[hit.man.Name]
	m.mu.Unlock()
	switch {
	case hit.err != nil:
		return nil, nil, fmt.Errorf("plugin %s is broken: %v", hit.man.Name, hit.err)
	case !info.Enabled:
		return nil, nil, fmt.Errorf("plugin %s: %w", hit.man.Name, ErrDisabled)
	case !info.Approved:
		return nil, nil, fmt.Errorf("plugin %s: %w (review its capabilities in Plugins)", hit.man.Name, ErrNotApproved)
	}
	if err := m.compile(ctx, hit); err != nil {
		return nil, nil, err
	}
	return hit, ps, nil
}

func (m *Manager) compile(ctx context.Context, l *loaded) error {
	h, err := m.host(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if l.pool != nil {
		return nil
	}
	module, err := os.ReadFile(filepath.Join(l.dir, l.man.Module))
	if err != nil {
		return err
	}
	cm, err := h.rt.CompileModule(context.WithoutCancel(ctx), module)
	if err != nil {
		return fmt.Errorf("compiling plugin %s: %w", l.man.Name, err)
	}
	l.pool = &pool{h: h, cm: cm}
	return nil
}

// wireRequest is the request as plugins see it.
type wireRequest struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url"`
	Headers []model.KeyValue  `json:"headers,omitempty"`
	Query   []model.KeyValue  `json:"query,omitempty"`
	Body    string            `json:"body,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

func toWire(r *model.Request) wireRequest {
	w := wireRequest{Kind: string(r.Kind), Name: r.Name, Method: r.Method, URL: r.URL, Headers: r.Headers, Query: r.Query, Options: r.Options}
	if r.Body != nil {
		w.Body = r.Body.Content
	}
	return w
}

// Execute runs a protocol plugin request. Timings.Total is measured here.
func (m *Manager) Execute(ctx context.Context, id string, req *model.Request) (*protocol.Response, []Log, error) {
	l, ps, err := m.find(ctx, KindProtocol, id)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	st := &callState{caps: l.man.Capabilities, plugin: l.man.Name, conns: map[int32]net.Conn{}}
	var r struct {
		Response *struct {
			Status     int              `json:"status"`
			StatusText string           `json:"statusText"`
			Headers    []model.KeyValue `json:"headers"`
			Body       []byte           `json:"body"`
			RemoteAddr string           `json:"remoteAddr"`
		} `json:"response"`
		Error string `json:"error"`
	}
	start := time.Now()
	err = l.pool.call(ctx, st, map[string]any{"op": "execute", "id": id, "request": toWire(req), "config": cfgOf(ps)}, &r)
	total := time.Since(start)
	if err != nil {
		return nil, st.logs, err
	}
	if r.Error != "" {
		return nil, st.logs, errors.New(r.Error)
	}
	if r.Response == nil {
		return nil, st.logs, errors.New("plugin returned no response")
	}
	res := &protocol.Response{
		Status: r.Response.Status, StatusText: r.Response.StatusText, Headers: r.Response.Headers,
		Body: r.Response.Body, Size: int64(len(r.Response.Body)), RemoteAddr: r.Response.RemoteAddr,
		Proto:   strings.ToUpper(id),
		Timings: protocol.Timings{Total: total},
	}
	if res.StatusText == "" && res.Status == 0 {
		res.StatusText = "OK"
	}
	return res, st.logs, nil
}

// Sign runs an auth plugin on a resolved request, updating its headers,
// query and URL in place.
func (m *Manager) Sign(ctx context.Context, id string, req *model.Request, params map[string]string) ([]Log, error) {
	l, ps, err := m.find(ctx, KindAuth, id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	st := &callState{caps: l.man.Capabilities, plugin: l.man.Name, conns: map[int32]net.Conn{}}
	var r struct {
		Request *wireRequest `json:"request"`
		Error   string       `json:"error"`
	}
	if err := l.pool.call(ctx, st, map[string]any{"op": "sign", "id": id, "request": toWire(req), "params": params, "config": cfgOf(ps)}, &r); err != nil {
		return st.logs, err
	}
	if r.Error != "" {
		return st.logs, errors.New(r.Error)
	}
	if r.Request != nil {
		req.Headers, req.Query, req.URL = r.Request.Headers, r.Request.Query, r.Request.URL
	}
	return st.logs, nil
}

func cfgOf(ps *pluginState) map[string]string {
	if ps == nil {
		return nil
	}
	return ps.Config
}

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultTimeout)
}

// Provider returns the provide entry for a kind/id, for editors.
func (m *Manager) Provider(kind, id string) (Provide, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range m.byNm {
		for _, p := range l.man.Provides {
			if p.Kind == kind && p.ID == id {
				return p, true
			}
		}
	}
	return Provide{}, false
}

// Close releases every instance and the runtime.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx := context.Background()
	for _, l := range m.byNm {
		if l.pool != nil {
			l.pool.close(ctx)
		}
	}
	if m.h != nil {
		m.h.close(ctx)
		m.h = nil
	}
}
