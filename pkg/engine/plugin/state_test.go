package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTwoManagersDontLoseWrites(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	if _, err := a.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.InstallDir(built["hmac"], false); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := a.SetConfig("tcp", map[string]string{"n": fmt.Sprint(i)})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = b.Approve("hmac")
			} else {
				_, err = b.SetEnabled("hmac", true)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	// One last write each way, so the final values are known.
	if _, err := a.SetConfig("tcp", map[string]string{"n": "last"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Approve("hmac"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetEnabled("hmac", true); err != nil {
		t.Fatal(err)
	}

	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	tcp, err := c.Get("tcp")
	if err != nil || tcp.Config["n"] != "last" {
		t.Fatalf("tcp = %+v, %v", tcp, err)
	}
	hmac, err := c.Get("hmac")
	if err != nil || !hmac.Approved || !hmac.Enabled {
		t.Fatalf("hmac = %+v, %v", hmac, err)
	}
}

func TestStaleLockIsBroken(t *testing.T) {
	m := manager(t)
	if _, err := m.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(m.Dir(), ".state.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if info, err := m.SetEnabled("tcp", false); err != nil || info.Enabled {
		t.Fatalf("SetEnabled = %+v, %v", info, err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock left behind: %v", err)
	}
}

func TestFreshLockTimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the lock timeout")
	}
	m := manager(t)
	if _, err := m.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(m.Dir(), ".state.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := m.SetEnabled("tcp", false)
	if err == nil || err.Error() != "plugin state is locked by another Pyro process" {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d < 4*time.Second || d > 8*time.Second {
		t.Fatalf("gave up after %v", d)
	}
	// A failed write must not change what the manager reports.
	if info, _ := m.Get("tcp"); !info.Enabled {
		t.Fatal("failed write changed in-memory state")
	}
}

func TestOpenSurvivesUnwritableMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the lock timeout")
	}
	dir := t.TempDir()
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	m.Close()
	// Legacy approval (no approvedMemoryMB), with a lock held by "another process".
	legacy := `{"plugins":{"tcp":{"enabled":true,"approved":{}}}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".state.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err = Open(dir)
	if err != nil {
		t.Fatalf("Open must not fail on an unwritable state: %v", err)
	}
	t.Cleanup(m.Close)
	if ps := m.st.Plugins["tcp"]; ps == nil || ps.ApprovedMemoryMB == 0 {
		t.Fatalf("migration not applied in memory: %+v", ps)
	}
}
