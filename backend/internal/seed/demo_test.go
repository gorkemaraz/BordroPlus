package seed

import (
	"context"
	"testing"
	"time"

	"bordroplus/internal/auth"
	"bordroplus/internal/testdb"
)

func TestDemoSeedIsIdempotentAndLoginable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	n, err := Demo(ctx, pool, "Demo.Parola.1")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(auth.Roles) {
		t.Fatalf("created %d accounts, want %d", n, len(auth.Roles))
	}
	// Second run (every compose start) changes nothing, even with a new password.
	if n, err := Demo(ctx, pool, "Another.Password.2"); err != nil || n != 0 {
		t.Fatalf("second run: created=%d err=%v", n, err)
	}

	// One account per role, and each can log in with the original password.
	svc := auth.NewService(pool, auth.NewTokens(make([]byte, 32), time.Minute),
		auth.Policy{RefreshTTL: time.Hour, MaxFailedLogins: 5, LockoutDuration: time.Minute})
	roles := map[auth.Role]bool{}
	for _, a := range DemoAccounts {
		sess, err := svc.Login(ctx, a.Email, "Demo.Parola.1", auth.Meta{})
		if err != nil {
			t.Fatalf("login %s: %v", a.Email, err)
		}
		roles[sess.Principal.Role] = true
	}
	if len(roles) != len(auth.Roles) {
		t.Fatalf("roles covered = %v", roles)
	}

	// The manager manages IT, which contains the software team and the demo employee.
	var inTeam bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM employees e
		    JOIN org_units eu ON eu.id = e.org_unit_id
		    JOIN org_units mu ON eu.path <@ mu.path
		    JOIN employees m  ON m.id = mu.manager_id
		   WHERE m.employee_no = 'D0002' AND e.employee_no = 'D0001')`).Scan(&inTeam)
	if err != nil || !inTeam {
		t.Fatalf("demo employee not in manager's sub-tree: %v %v", inTeam, err)
	}

	var audits int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'seed.demo_accounts'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("seed audit rows = %d, want 1", audits)
	}
}
