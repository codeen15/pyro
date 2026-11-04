package db

import (
	"time"

	"github.com/jackc/pgx/v5"
)

const connectTimeout = 10 * time.Second

func pgxConfig(url string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, redact(err, url)
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = connectTimeout
	}
	return cfg, nil
}
