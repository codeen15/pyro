package history

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"
)

// Cookies from the interactive jar are kept per workspace, so a login
// survives restarting pyro serve.
type cookieRow struct {
	bun.BaseModel `bun:"table:cookies"`
	Workspace     string `bun:"workspace,pk"`
	Data          []byte `bun:"data,notnull"`
}

// SaveCookies stores the workspace's cookie jar (JSON).
func (s *Store) SaveCookies(ctx context.Context, workspace string, data []byte) error {
	_, err := s.db.NewInsert().Model(&cookieRow{Workspace: workspace, Data: data}).
		On("CONFLICT (workspace) DO UPDATE").Set("data = EXCLUDED.data").Exec(ctx)
	return err
}

// LoadCookies returns the saved jar, or nil if there is none.
func (s *Store) LoadCookies(ctx context.Context, workspace string) ([]byte, error) {
	var row cookieRow
	err := s.db.NewSelect().Model(&row).Where("workspace = ?", workspace).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return row.Data, err
}
