package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/testdb"
)

func insertUnit(t *testing.T, pool *pgxpool.Pool, parent *int64, code string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO org_units (parent_id, code, name) VALUES ($1, $2, $2) RETURNING id`, parent, code).Scan(&id)
	if err != nil {
		t.Fatalf("insert %s: %v", code, err)
	}
	return id
}

func unitPath(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var p string
	if err := pool.QueryRow(context.Background(), `SELECT path::text FROM org_units WHERE id = $1`, id).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestOrgUnitPathsFollowMoves(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	root := insertUnit(t, pool, nil, "gm")
	it := insertUnit(t, pool, &root, "it")
	dev := insertUnit(t, pool, &it, "dev")
	fin := insertUnit(t, pool, &root, "fin")

	if got := unitPath(t, pool, dev); got != "gm.it.dev" {
		t.Fatalf("dev path = %q", got)
	}

	// Moving "it" under "fin" must rewrite the whole subtree.
	if _, err := pool.Exec(ctx, `UPDATE org_units SET parent_id = $1 WHERE id = $2`, fin, it); err != nil {
		t.Fatal(err)
	}
	if got := unitPath(t, pool, dev); got != "gm.fin.it.dev" {
		t.Fatalf("dev path after move = %q", got)
	}

	// Renaming a code also cascades.
	if _, err := pool.Exec(ctx, `UPDATE org_units SET code = 'finans' WHERE id = $1`, fin); err != nil {
		t.Fatal(err)
	}
	if got := unitPath(t, pool, dev); got != "gm.finans.it.dev" {
		t.Fatalf("dev path after rename = %q", got)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM org_units WHERE path <@ 'gm.finans'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("subtree size = %d, want 3", n)
	}
}

func TestOrgUnitRejectsCyclesAndSecondRoot(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	root := insertUnit(t, pool, nil, "gm")
	it := insertUnit(t, pool, &root, "it")
	dev := insertUnit(t, pool, &it, "dev")

	_, err := pool.Exec(ctx, `UPDATE org_units SET parent_id = $1 WHERE id = $2`, dev, it)
	if pgCode(err) != "23514" {
		t.Fatalf("moving under own subtree: err = %v, want check_violation", err)
	}

	_, err = pool.Exec(ctx, `INSERT INTO org_units (code, name) VALUES ('other_root', 'x')`)
	if pgCode(err) != "23505" {
		t.Fatalf("second root: err = %v, want unique_violation", err)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO audit_log (action, entity_type) VALUES ('test.write', 'test')`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE audit_log SET action = 'tampered'`,
		`DELETE FROM audit_log`,
		`TRUNCATE audit_log`,
	} {
		if _, err := pool.Exec(ctx, stmt); pgCode(err) != "42501" {
			t.Errorf("%s: err = %v, want insufficient_privilege", stmt, err)
		}
	}
}

func TestUserConstraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	const hash = "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA"

	cases := []struct {
		name, email, hash, role string
	}{
		{"unknown role", "a@x.test", hash, "admin"},
		{"employee without employee record", "b@x.test", hash, "employee"},
		{"bad e-mail", "not-an-email", hash, "hr"},
		{"non-argon2 hash", "c@x.test", "plaintext", "hr"},
	}
	for _, c := range cases {
		_, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3)`, c.email, c.hash, c.role)
		if pgCode(err) != "23514" {
			t.Errorf("%s: err = %v, want check_violation", c.name, err)
		}
	}

	if _, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash, role) VALUES ('HR@x.test', $1, 'hr')`, hash); err != nil {
		t.Fatal(err)
	}
	// citext: e-mail uniqueness ignores case.
	_, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash, role) VALUES ('hr@X.test', $1, 'hr')`, hash)
	if pgCode(err) != "23505" {
		t.Fatalf("duplicate e-mail: err = %v, want unique_violation", err)
	}
}
