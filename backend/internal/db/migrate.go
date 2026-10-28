package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"bordroplus/migrations"
)

// Migrate applies every embedded migration to the database at url and
// returns the resulting schema version.
func Migrate(ctx context.Context, url string) (int64, error) {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return 0, err
	}
	defer sqlDB.Close()

	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return 0, fmt.Errorf("goose provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return 0, fmt.Errorf("migrate up: %w", err)
	}
	return p.GetDBVersion(ctx)
}
