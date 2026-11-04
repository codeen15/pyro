// Package db opens Pyro's database: SQLite (the default, one file, pure Go)
// or PostgreSQL (for a hosted team server), behind the Bun ORM so the same
// code runs on both.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

// DB is a Bun database handle.
type DB = bun.DB

// Open connects to url:
//
//	postgres://user:pass@host:5432/pyro?sslmode=require   PostgreSQL
//	sqlite:///var/lib/pyro/pyro.db, sqlite:pyro.db, or a plain file path   SQLite
func Open(url string) (*bun.DB, error) {
	switch {
	case strings.HasPrefix(url, "postgres://"), strings.HasPrefix(url, "postgresql://"):
		cfg, err := pgxConfig(url)
		if err != nil {
			return nil, err
		}
		sqldb := stdlib.OpenDB(*cfg)
		db := bun.NewDB(sqldb, pgdialect.New())
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("connect to PostgreSQL: %w", redact(err, url))
		}
		return db, nil
	case url == "":
		return nil, errors.New("no database given")
	default:
		file := strings.TrimPrefix(strings.TrimPrefix(url, "sqlite://"), "sqlite:")
		if dir := filepath.Dir(file); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
		}
		sqldb, err := sql.Open("sqlite", "file:"+file+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
		if err != nil {
			return nil, err
		}
		// SQLite allows one writer; a single connection avoids "database is locked".
		sqldb.SetMaxOpenConns(1)
		return bun.NewDB(sqldb, sqlitedialect.New()), nil
	}
}

// IsPostgres reports whether db is PostgreSQL.
func IsPostgres(db bun.IDB) bool {
	return db.Dialect().Name().String() == "pg"
}

// redact keeps a password in the URL out of error messages.
func redact(err error, url string) error {
	msg := err.Error()
	if at := strings.LastIndex(url, "@"); at > 0 {
		if i := strings.Index(url, "://"); i >= 0 {
			if colon := strings.Index(url[i+3:at], ":"); colon >= 0 {
				pw := url[i+3+colon+1 : at]
				if pw != "" {
					msg = strings.ReplaceAll(msg, pw, "****")
				}
			}
		}
	}
	return errors.New(msg)
}

// Migration is one schema step. Steps run once, in order, recorded in the
// pyro_migrations table under "<set>:<name>".
type Migration struct {
	Name string
	Up   func(ctx context.Context, tx bun.Tx) error
}

type migrationRow struct {
	bun.BaseModel `bun:"table:pyro_migrations"`
	Name          string `bun:"name,pk"`
}

// Migrate applies a package's migrations that haven't run yet.
func Migrate(ctx context.Context, db *bun.DB, set string, steps []Migration) error {
	if _, err := db.NewCreateTable().Model((*migrationRow)(nil)).IfNotExists().Exec(ctx); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	for _, m := range steps {
		key := set + ":" + m.Name
		n, err := db.NewSelect().Model((*migrationRow)(nil)).Where("name = ?", key).Count(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if err := m.Up(ctx, tx); err != nil {
				return err
			}
			_, err := tx.NewInsert().Model(&migrationRow{Name: key}).Exec(ctx)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", key, err)
		}
	}
	return nil
}

// CreateTables creates each model's table (and nothing if it exists).
func CreateTables(ctx context.Context, tx bun.IDB, models ...any) error {
	for _, m := range models {
		if _, err := tx.NewCreateTable().Model(m).IfNotExists().Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// CreateIndex adds an index if it doesn't exist. Columns are SQL as written
// (e.g. "id DESC", or `"user"` for a reserved word).
func CreateIndex(ctx context.Context, tx bun.IDB, model any, name string, columns ...string) error {
	q := tx.NewCreateIndex().Model(model).Index(name).IfNotExists()
	for _, c := range columns {
		q = q.ColumnExpr(c)
	}
	_, err := q.Exec(ctx)
	return err
}
