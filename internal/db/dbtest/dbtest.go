// Package dbtest gives tests a fresh database (SQLite, or PostgreSQL with
// $PYRO_TEST_POSTGRES_URL).
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"github.com/codeen15/pyro/internal/db"
)

// Open opens a fresh, empty database for a test: SQLite in a temp
// folder, or a new schema in PostgreSQL when $PYRO_TEST_POSTGRES_URL is set
// (dropped when the test ends).
func Open(t testing.TB) *bun.DB {
	t.Helper()
	url := os.Getenv("PYRO_TEST_POSTGRES_URL")
	if url == "" {
		d, err := db.Open(filepath.Join(t.TempDir(), "pyro.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		return d
	}
	admin, err := db.Open(url)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "t_" + hex.EncodeToString(b)
	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	d, err := db.Open(url + sep + "search_path=" + schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Close()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return d
}
