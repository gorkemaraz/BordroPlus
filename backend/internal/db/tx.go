package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is satisfied by *pgxpool.Pool, *pgxpool.Conn and pgx.Tx, so data
// access functions work both inside and outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// InTx runs fn in a transaction, committing on nil and rolling back otherwise.
func InTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsUniqueViolation reports whether err is a unique_violation, optionally on
// a specific constraint.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
