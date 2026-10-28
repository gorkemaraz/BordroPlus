// Package testdb gives integration tests a freshly migrated PostgreSQL
// database. It needs TEST_DATABASE_URL pointing at a server where the user
// may CREATE DATABASE; without it the calling test is skipped.
//
//	make testdb-up   # local throwaway Postgres on 127.0.0.1:55432
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/db"
)

// New creates a database named after a random suffix, applies all
// migrations and drops it when the test finishes.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "bp_test_" + hex.EncodeToString(b[:])

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		conn.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	conn.Close(ctx)

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()

	if _, err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 20
	cfg.ConnConfig.RuntimeParams["timezone"] = "Europe/Istanbul"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Logf("drop %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})
	return pool
}
