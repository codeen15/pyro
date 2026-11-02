package store

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/load"
	"github.com/codeen15/pyro/pkg/engine/runner"
)

const (
	loadtestsDir = "loadtests"
	LoadExt      = ".load.yaml"
)

// ListLoadTests returns load test names (file names without .load.yaml).
func (w *Workspace) ListLoadTests() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(w.Root, loadtestsDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), LoadExt) {
			names = append(names, strings.TrimSuffix(e.Name(), LoadExt))
		}
	}
	sort.Strings(names)
	return names, nil
}

func (w *Workspace) loadFile(name string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("%w: load test name %q", ErrInvalidPath, name)
	}
	return w.resolve(loadtestsDir, name+LoadExt)
}

// ReadLoadTest returns the raw YAML so comments and layout survive edits.
func (w *Workspace) ReadLoadTest(name string) ([]byte, error) {
	file, err := w.loadFile(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}

// WriteLoadTest stores raw YAML after validating it parses as a plan.
func (w *Workspace) WriteLoadTest(name string, data []byte) error {
	file, err := w.loadFile(name)
	if err != nil {
		return err
	}
	if _, err := load.ParsePlan(data, w.RequestExists); err != nil {
		return &ValidationError{Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return writeRaw(file, data)
}

func (w *Workspace) DeleteLoadTest(name string) error {
	file, err := w.loadFile(name)
	if err != nil {
		return err
	}
	if err := os.Remove(file); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

// LoadPlan reads and validates a load test.
func (w *Workspace) LoadPlan(name string) (*load.Plan, error) {
	data, err := w.ReadLoadTest(name)
	if err != nil {
		return nil, err
	}
	p, err := load.ParsePlan(data, w.RequestExists)
	if err != nil {
		return nil, &ValidationError{Err: err}
	}
	if err := load.InlineScripts(p, w.ReadFile); err != nil {
		return nil, &ValidationError{Err: err}
	}
	return p, nil
}

// ReadFile reads a workspace file by its slash-separated relative path
// (refusing paths that leave the workspace).
func (w *Workspace) ReadFile(rel string) ([]byte, error) {
	p, err := w.resolve(filepath.ToSlash(filepath.Clean(rel)))
	if err != nil {
		return nil, fmt.Errorf("%q must be inside the workspace: %w", rel, err)
	}
	return os.ReadFile(p)
}

// RequestExists reports whether a request path exists.
func (w *Workspace) RequestExists(path string) bool {
	_, err := w.ReadRequest(path)
	return err == nil
}

// Target resolves a request path for the load engine.
func (w *Workspace) Target(path string) (load.Target, error) {
	req, err := w.ReadRequest(path)
	if err != nil {
		return load.Target{}, err
	}
	chain, err := w.Chain(path)
	if err != nil {
		return load.Target{}, err
	}
	return load.Target{Path: path, Request: req, Chain: chain}, nil
}

// LoadData reads a data file named by a plan, relative to the workspace root.
func (w *Workspace) LoadData(file string) ([]map[string]string, error) {
	p, err := w.resolve(filepath.ToSlash(filepath.Clean(file)))
	if err != nil {
		return nil, fmt.Errorf("data file %q must be inside the workspace: %w", file, err)
	}
	return runner.LoadData(p)
}

// UnconfirmedHosts returns the hosts that are neither loopback nor in
// pyro.yaml's loadAllowHosts; a load test against them needs confirmation.
func (w *Workspace) UnconfirmedHosts(hosts []string) []string {
	allowed := map[string]bool{}
	for _, h := range w.Meta.LoadAllowHosts {
		allowed[strings.ToLower(h)] = true
	}
	var out []string
	for _, h := range hosts {
		if h == "localhost" || allowed[strings.ToLower(h)] {
			continue
		}
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			continue
		}
		out = append(out, h)
	}
	return out
}

// ValidationError wraps an invalid load plan so the API can answer 400.
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

func writeRaw(file string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), file)
}
