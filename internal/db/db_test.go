package db

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenSQLiteForms(t *testing.T) {
	dir := t.TempDir()
	for _, url := range []string{filepath.Join(dir, "a.db"), "sqlite:" + filepath.Join(dir, "b.db"), "sqlite://" + filepath.Join(dir, "sub", "c.db")} {
		d, err := Open(url)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if IsPostgres(d) {
			t.Fatalf("%s opened as PostgreSQL", url)
		}
		if err := d.Ping(); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		d.Close()
	}
}

func TestRedactsPassword(t *testing.T) {
	err := redact(errors.New(`failed for postgres://pyro:s3cret@db/pyro: password s3cret wrong`), "postgres://pyro:s3cret@db/pyro")
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("password leaked: %v", err)
	}
}
