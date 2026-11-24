package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The CLI and a running server are separate processes sharing one plugins
// folder. Reload brings a manager up to date with what is on disk; Watch
// polls for changes (no fsnotify: pure Go, and the folder is small).
//
// Runtimes (hosts) are kept until Close even when no plugin uses their
// memory limit any more: there is one per distinct limit, an empty runtime
// holds no plugin code (a retired pool closes its compiled module), and a
// call still in flight on a retired pool runs inside it.

// fingerprint identifies the installed files of one plugin: the manifest's
// and module's modification times (ns) and the module's size.
type fingerprint struct{ manifest, module, size int64 }

func fingerprintOf(dir string, man *Manifest) fingerprint {
	var fp fingerprint
	if fi, err := os.Stat(filepath.Join(dir, ManifestFile)); err == nil {
		fp.manifest = fi.ModTime().UnixNano()
	}
	if man != nil && man.Module != "" {
		if fi, err := os.Stat(filepath.Join(dir, man.Module)); err == nil {
			fp.module, fp.size = fi.ModTime().UnixNano(), fi.Size()
		}
	}
	return fp
}

// scan reads every installed plugin's manifest under dir. A folder whose
// manifest can't be read becomes a broken placeholder named after it.
func scan(dir string) (map[string]*loaded, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*loaded{}
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
			l.man = &Manifest{Name: e.Name(), Limits: Limits{MemoryMB: DefaultMemoryMBWasm}}
			l.fp = fingerprintOf(l.dir, nil)
		} else {
			l.fp = fingerprintOf(l.dir, l.man)
		}
		out[l.man.Name] = l
	}
	return out, nil
}

// migrateApprovals updates approvals saved before memory limits were part
// of approval: the user approved what the manifest (memOf) asks for. It
// returns the plugins it changed. It runs on every state read (Open,
// Reload, mutate), so a migration that couldn't be saved isn't lost when
// the state is read again.
func migrateApprovals(st *state, memOf func(name string) (int, bool)) map[string]int {
	migrated := map[string]int{}
	for name, ps := range st.Plugins {
		if ps == nil || ps.Approved == nil || ps.ApprovedMemoryMB != 0 {
			continue
		}
		if mb, ok := memOf(name); ok {
			ps.ApprovedMemoryMB = mb
			migrated[name] = mb
		}
	}
	return migrated
}

// memIn returns the memory limit of each working plugin in byNm.
func memIn(byNm map[string]*loaded) func(string) (int, bool) {
	return func(name string) (int, bool) {
		l := byNm[name]
		if l == nil || l.err != nil {
			return 0, false
		}
		return l.man.Limits.MemoryMB, true
	}
}

// saveMigration writes migrated approvals; failing to is only logged,
// since the in-memory migration is right either way (a read-only plugins
// folder must not disable every plugin). The caller holds m.mu.
func (m *Manager) saveMigration(migrated map[string]int) {
	if len(migrated) == 0 {
		return
	}
	err := m.mutate(func(st *state) error {
		for name, mb := range migrated {
			if ps := st.Plugins[name]; ps != nil && ps.Approved != nil && ps.ApprovedMemoryMB == 0 {
				ps.ApprovedMemoryMB = mb
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("plugins: could not save the approval migration", "dir", m.dir, "err", err)
	}
}

// Reload rescans the plugins folder and re-reads state.json, picking up
// changes made by another process: new plugins are loaded, removed ones
// dropped, and plugins whose files changed replaced. Calls in flight on a
// dropped or replaced plugin finish on their old instance; later calls use
// the new code. Subscribers are notified when anything changed.
func (m *Manager) Reload() (changed bool, err error) {
	m.mu.Lock()
	changed, err = m.reload()
	m.mu.Unlock()
	if changed {
		m.notify()
	}
	return changed, err
}

func (m *Manager) reload() (bool, error) {
	st, err := readState(m.dir)
	if err != nil {
		return false, err
	}
	found, err := scan(m.dir)
	if err != nil {
		return false, err
	}
	// A legacy approval covers the manifest this manager loaded it with,
	// or (for a plugin new to it) the one now on disk.
	oldMem, newMem := memIn(m.byNm), memIn(found)
	migrated := migrateApprovals(&st, func(name string) (int, bool) {
		if mb, ok := oldMem(name); ok {
			return mb, true
		}
		return newMem(name)
	})

	changed := false
	for name, l := range m.byNm {
		if nl := found[name]; nl == nil || nl.fp != l.fp || nl.dir != l.dir {
			if l.pool != nil {
				l.pool.retire()
			}
			delete(m.byNm, name)
			changed = true
		}
	}
	added := map[string]*loaded{}
	for name, nl := range found {
		if m.byNm[name] == nil {
			m.byNm[name] = nl
			added[name] = nl
			changed = true
		}
	}
	m.precompileFound(added)
	if !sameState(m.st, st) {
		changed = true
	}
	m.st = st
	m.saveMigration(migrated)
	return changed, nil
}

func sameState(a, b state) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

// snapshot is what Watch compares between polls: state.json's mtime and
// size, and every plugin folder's fingerprint.
type snapshot struct {
	state      [2]int64
	plugins    map[string]fingerprint
	unreadable bool
}

func (m *Manager) snapshot() snapshot {
	var s snapshot
	if fi, err := os.Stat(filepath.Join(m.dir, stateFile)); err == nil {
		s.state = [2]int64{fi.ModTime().UnixNano(), fi.Size()}
	}
	found, err := scan(m.dir)
	if err != nil {
		s.unreadable = true
		return s
	}
	s.plugins = make(map[string]fingerprint, len(found))
	for name, l := range found {
		s.plugins[name] = l.fp
	}
	return s
}

func (s snapshot) equal(o snapshot) bool {
	return s.state == o.state && s.unreadable == o.unreadable && maps.Equal(s.plugins, o.plugins)
}

// Watch polls the plugins folder every interval until ctx ends, calling
// Reload (which notifies subscribers) when state.json or any plugin's
// files changed. It blocks.
func (m *Manager) Watch(ctx context.Context, every time.Duration) {
	last := m.snapshot()
	// Catch up on anything that changed between Open and now.
	if _, err := m.Reload(); err != nil {
		slog.Warn("plugins: reload failed", "dir", m.dir, "err", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := m.snapshot()
		if cur.equal(last) {
			continue
		}
		if _, err := m.Reload(); err != nil {
			// Keep last as it was so the next tick tries again.
			slog.Warn("plugins: reload failed", "dir", m.dir, "err", err)
			continue
		}
		last = cur
	}
}

// Subscribe registers f to run after the installed plugins or their state
// change, whether in this process (install, remove, approve, enable,
// settings) or another (seen by Reload). f runs outside the manager's
// locks, so it may call back into the manager. cancel unregisters it.
func (m *Manager) Subscribe(f func()) (cancel func()) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	if m.subs == nil {
		m.subs = map[int]func(){}
	}
	m.nextSub++
	id := m.nextSub
	m.subs[id] = f
	return func() {
		m.subMu.Lock()
		defer m.subMu.Unlock()
		delete(m.subs, id)
	}
}

// notify runs every subscriber. The caller must not hold m.mu.
func (m *Manager) notify() {
	m.subMu.Lock()
	fs := make([]func(), 0, len(m.subs))
	for _, f := range m.subs {
		fs = append(fs, f)
	}
	m.subMu.Unlock()
	for _, f := range fs {
		f()
	}
}
