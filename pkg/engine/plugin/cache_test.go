package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// OpenWithCache keeps compiled code in the given folder, not <dir>/cache.
func TestOpenWithCache(t *testing.T) {
	saved := testCacheDir
	testCacheDir = "" // the suite-wide override would hide the setting
	t.Cleanup(func() { testCacheDir = saved })

	dir, cache := t.TempDir(), filepath.Join(t.TempDir(), "shared-cache")
	m, err := OpenWithCache(dir, cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.InstallDir(built["tcp"], true); err != nil {
		t.Fatal(err)
	}
	m.Close()

	if entries, err := os.ReadDir(cache); err != nil || len(entries) == 0 {
		t.Errorf("custom cache folder is empty: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); err == nil {
		t.Error("the default <dir>/cache was used as well")
	}
}
