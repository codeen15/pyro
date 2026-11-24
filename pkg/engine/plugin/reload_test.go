package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pair opens two managers on one folder, as the CLI and a running server do.
func pair(t *testing.T) (a, b *Manager) {
	t.Helper()
	dir := t.TempDir()
	var err error
	if a, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if b, err = Open(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return a, b
}

func TestReloadSeesOtherProcessInstall(t *testing.T) {
	a, b := pair(t)
	if _, err := b.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	changed, err := a.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload = %v, %v; want changed", changed, err)
	}
	addr := echoServer(t)
	res, _, err := a.Execute(context.Background(), "tcp", tcpReq(addr, "hi\n", nil))
	if err != nil || !strings.Contains(string(res.Body), "echo:hi") {
		t.Fatalf("Execute after Reload = %+v, %v", res, err)
	}
	// Nothing changed since: a second Reload reports no change.
	if changed, err := a.Reload(); err != nil || changed {
		t.Fatalf("second Reload = %v, %v; want unchanged", changed, err)
	}
}

func TestReloadSeesRemoveAndApprove(t *testing.T) {
	a, b := pair(t)
	if _, err := b.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.InstallDir(built["hmac"], false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	if info, err := a.Get("hmac"); err != nil || info.Approved {
		t.Fatalf("hmac before approval = %+v, %v", info, err)
	}

	if err := b.Remove("tcp"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Approve("hmac"); err != nil {
		t.Fatal(err)
	}
	if changed, err := a.Reload(); err != nil || !changed {
		t.Fatalf("Reload = %v, %v; want changed", changed, err)
	}
	if _, err := a.Get("tcp"); err != ErrNotFound {
		t.Fatalf("removed plugin still listed: %v", err)
	}
	if info, err := a.Get("hmac"); err != nil || !info.Approved {
		t.Fatalf("hmac after approval = %+v, %v", info, err)
	}
}

func TestReloadDuringCall(t *testing.T) {
	a, b := pair(t)
	installAlloc(t, b, "{ maxInstances: 2 }")
	if _, err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	// Compile and warm A's pool so the slow call below uses it.
	if _, _, err := a.Execute(context.Background(), "alloc", allocReq(nil)); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	old := a.byNm["alloc"]
	a.mu.Unlock()
	live := func() int {
		old.pool.mu.Lock()
		defer old.pool.mu.Unlock()
		return old.pool.live
	}

	// The new version goes on disk first; A only sees it at Reload.
	installAlloc(t, b, "{ maxInstances: 3 }")
	done := make(chan error, 1)
	go func() {
		_, _, err := a.Execute(context.Background(), "alloc", allocReq(map[string]string{"sleepMs": "1000"}))
		done <- err
	}()
	// Wait until the call holds an instance of the old pool.
	for deadline := time.Now().Add(5 * time.Second); live() != 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the slow call never started")
		}
	}
	if changed, err := a.Reload(); err != nil || !changed {
		t.Fatalf("Reload = %v, %v; want changed", changed, err)
	}
	// Reload ran while the call was in flight.
	select {
	case err := <-done:
		t.Fatalf("the call finished before Reload returned (err %v); the test didn't overlap them", err)
	default:
	}
	if n := live(); n != 1 {
		t.Fatalf("old pool live = %d right after Reload, want the in-flight call's 1", n)
	}
	if err := <-done; err != nil {
		t.Fatalf("in-flight call failed across Reload: %v", err)
	}
	if _, _, err := a.Execute(context.Background(), "alloc", allocReq(nil)); err != nil {
		t.Fatalf("call after Reload: %v", err)
	}
	a.mu.Lock()
	cur := a.byNm["alloc"]
	a.mu.Unlock()
	if cur == old || cur.man.Limits.MaxInstances != 3 {
		t.Fatalf("Reload kept the old plugin (maxInstances %d)", cur.man.Limits.MaxInstances)
	}
	// The old pool closed the instance that came back and its module.
	old.pool.mu.Lock()
	defer old.pool.mu.Unlock()
	if len(old.pool.idle) != 0 || old.pool.live != 0 || !old.pool.cmClosed {
		t.Fatalf("retired pool leaked: idle=%d live=%d cmClosed=%v", len(old.pool.idle), old.pool.live, old.pool.cmClosed)
	}
}

func TestRetiredPoolClosesReturnedInstances(t *testing.T) {
	m := manager(t)
	installAlloc(t, m, "{ maxInstances: 2 }")
	l, _, err := m.find(context.Background(), KindProtocol, "alloc")
	if err != nil {
		t.Fatal(err)
	}
	p := l.pool
	busy, err := p.get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	idle, err := p.get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.put(idle)
	p.retire()
	if len(p.idle) != 0 || p.cmClosed {
		t.Fatalf("after retire: idle=%d cmClosed=%v (a call is still busy)", len(p.idle), p.cmClosed)
	}
	p.put(busy)
	if len(p.idle) != 0 || p.live != 0 || !p.cmClosed {
		t.Fatalf("returned instance kept: idle=%d live=%d cmClosed=%v", len(p.idle), p.live, p.cmClosed)
	}
	if _, err := p.get(context.Background()); err == nil {
		t.Fatal("get on a retired, drained pool must fail")
	}
}

func TestWatchNotifies(t *testing.T) {
	a, b := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notified := make(chan struct{}, 10)
	unsub := a.Subscribe(func() { notified <- struct{}{} })
	defer unsub()
	go a.Watch(ctx, 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	if _, err := b.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("no notification within 1s of another process installing")
	}
	if _, err := a.Get("tcp"); err != nil {
		t.Fatalf("watched manager doesn't list tcp: %v", err)
	}
}

func TestSubscribeSeesLocalMutations(t *testing.T) {
	m := manager(t)
	n := 0
	cancel := m.Subscribe(func() {
		n++
		m.List() // callbacks may call back into the manager
	})
	if _, err := m.InstallDir(built["tcp"], false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Approve("tcp"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("tcp"); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("notified %d times, want 3", n)
	}
	cancel()
	if _, err := m.InstallDir(built["tcp"], false); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatal("cancelled subscriber still notified")
	}
}

func TestReloadAppliesLegacyMigration(t *testing.T) {
	a, b := pair(t)
	if _, err := b.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	// The installed manifest asks for 256 MB; another (older) process
	// rewrote the state without approvedMemoryMB.
	installed := filepath.Join(a.Dir(), "tcp", ManifestFile)
	manifest, _ := os.ReadFile(installed)
	if err := os.WriteFile(installed, append(manifest, []byte("\nlimits: { memoryMB: 256 }\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := `{"plugins":{"tcp":{"enabled":true,"approved":{"network":["*:*"]}}}}`
	if err := os.WriteFile(filepath.Join(a.Dir(), "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	if info, _ := a.Get("tcp"); !info.Approved {
		t.Fatalf("legacy approval lost on Reload: %+v", info)
	}
	if data, _ := os.ReadFile(filepath.Join(a.Dir(), "state.json")); !strings.Contains(string(data), `"approvedMemoryMB": 256`) {
		t.Fatalf("migration not persisted: %s", data)
	}
	// A write that re-reads the legacy state keeps the approval too.
	if err := os.WriteFile(filepath.Join(a.Dir(), "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := a.SetConfig("tcp", map[string]string{"x": "1"}); err != nil || !info.Approved {
		t.Fatalf("legacy approval lost on write: %+v, %v", info, err)
	}
}

// TestCallRetriesAfterReload: a plugin replaced between the lookup and the
// call (its pool retired, with no instance out) is looked up again, and the
// call goes to the new version instead of failing with "try again".
func TestCallRetriesAfterReload(t *testing.T) {
	m := manager(t)
	installAlloc(t, m, "{ maxInstances: 2 }")
	replaced := false
	var r struct {
		Error string `json:"error"`
	}
	_, _, err := m.invoke(context.Background(), KindProtocol, "alloc", func(l *loaded) error {
		if !replaced {
			replaced = true
			installAlloc(t, m, "{ maxInstances: 3 }") // retires l's pool
		}
		return nil
	}, func(*pluginState) any {
		return map[string]any{"op": "execute", "id": "alloc", "request": toWire(allocReq(nil))}
	}, &r)
	if err != nil || r.Error != "" {
		t.Fatalf("call across a reinstall = %v, %q", err, r.Error)
	}
	if !replaced {
		t.Fatal("the check never ran")
	}
	// Without the retry, the same race fails with ErrReloaded.
	replaced = false
	_, _, err = m.invokeOnce(context.Background(), KindProtocol, "alloc", func(l *loaded) error {
		if !replaced {
			replaced = true
			installAlloc(t, m, "{ maxInstances: 2 }")
		}
		return nil
	}, func(*pluginState) any {
		return map[string]any{"op": "execute", "id": "alloc", "request": toWire(allocReq(nil))}
	}, &r)
	if !errors.Is(err, ErrReloaded) {
		t.Fatalf("one attempt across a reinstall: err = %v, want ErrReloaded", err)
	}
}
