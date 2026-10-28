// Command migrate applies the embedded database migrations.
//
//	migrate up       apply all pending migrations (default)
//	migrate status   print migration status
//	migrate down     roll back the latest migration
//
// In docker compose it runs as a one-shot service before the API starts.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"bordroplus/internal/config"
	"bordroplus/internal/db"
	"bordroplus/internal/logging"
	"bordroplus/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, cfg.LogLevel)

	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Wait for the database with the same retry logic as the API.
	pool, err := db.Connect(ctx, cfg.DatabaseURL, 2, cfg.DBConnectTimeout, log)
	if err != nil {
		return err
	}
	pool.Close()

	sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	switch cmd {
	case "up":
		if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
			return err
		}
	case "down":
		if err := goose.DownContext(ctx, sqlDB, "."); err != nil {
			return err
		}
	case "status":
		return goose.StatusContext(ctx, sqlDB, ".")
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}

	v, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return err
	}
	log.Info("migrations applied", "command", cmd, "version", v)
	return nil
}
