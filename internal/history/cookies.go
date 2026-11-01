package history

import (
	"context"
	"database/sql"
	"errors"
)

// Cookies from the interactive jar are kept per workspace, so a login
// survives restarting pyro serve.
const cookiesSchema = `
CREATE TABLE IF NOT EXISTS cookies (
	workspace TEXT PRIMARY KEY,
	data      BLOB NOT NULL
);`

func (s *Store) initCookies() error {
	_, err := s.db.Exec(cookiesSchema)
	return err
}

// SaveCookies stores the workspace's cookie jar (JSON).
func (s *Store) SaveCookies(ctx context.Context, workspace string, data []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cookies(workspace, data) VALUES(?, ?)
		ON CONFLICT(workspace) DO UPDATE SET data = excluded.data`, workspace, data)
	return err
}

// LoadCookies returns the saved jar, or nil if there is none.
func (s *Store) LoadCookies(ctx context.Context, workspace string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM cookies WHERE workspace = ?`, workspace).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return data, err
}
