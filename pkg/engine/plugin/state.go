package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	stateFile = "state.json"
	lockFile  = ".state.lock"

	lockRetry   = 20 * time.Millisecond
	lockStale   = 10 * time.Second
	lockTimeout = 5 * time.Second
)

// ErrStateLocked: another Pyro process held the state lock for too long.
var ErrStateLocked = errors.New("plugin state is locked by another Pyro process")

// state is persisted in <dir>/state.json.
type state struct {
	Plugins map[string]*pluginState `json:"plugins"`
}

type pluginState struct {
	Enabled bool `json:"enabled"`
	// Approved is the capability set the user approved; a plugin whose
	// manifest asks for anything different needs approval again.
	Approved *Capabilities `json:"approved,omitempty"`
	// ApprovedMemoryMB is the memory limit the user approved; 0 (state
	// written before limits were approved) means the old default, 128.
	ApprovedMemoryMB int               `json:"approvedMemoryMB,omitempty"`
	Config           map[string]string `json:"config,omitempty"`
}

// approvedMem returns the memory limit ps covers.
func (ps *pluginState) approvedMem() int {
	if ps.ApprovedMemoryMB == 0 {
		return DefaultMemoryMBWasm
	}
	return ps.ApprovedMemoryMB
}

// readState reads <dir>/state.json; a missing file is an empty state.
func readState(dir string) (state, error) {
	empty := state{Plugins: map[string]*pluginState{}}
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("plugin state: %w", err)
	}
	st := state{}
	if err := json.Unmarshal(data, &st); err != nil {
		return empty, fmt.Errorf("plugin state: %w", err)
	}
	if st.Plugins == nil {
		st.Plugins = map[string]*pluginState{}
	}
	return st, nil
}

// writeState replaces <dir>/state.json atomically.
func writeState(dir string, st state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, stateFile)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// lockState takes the cross-process lock on the plugins folder: a file
// created with O_EXCL, which works the same on every platform. A lock older
// than lockStale belongs to a crashed process and is removed. It returns the
// release function.
func lockState(dir string) (func(), error) {
	path := filepath.Join(dir, lockFile)
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > lockStale {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, ErrStateLocked
		}
		time.Sleep(lockRetry)
	}
}

// mutate applies f to the state on disk: under the lock it re-reads
// state.json (so changes made by another process are kept), runs f, writes
// the result and makes it the manager's current state. If f or the write
// fails the manager's state is left as it was. The caller holds m.mu (or is
// still opening the manager).
func (m *Manager) mutate(f func(st *state) error) error {
	unlock, err := lockState(m.dir)
	if err != nil {
		return err
	}
	defer unlock()
	st, err := readState(m.dir)
	if err != nil {
		return err
	}
	if err := f(&st); err != nil {
		return err
	}
	if err := writeState(m.dir, st); err != nil {
		return err
	}
	m.st = st
	return nil
}
