package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/testdb"
)

const testPassword = "Test.Parola.123"

func newTestService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	tokens := NewTokens([]byte(strings.Repeat("k", 32)), 15*time.Minute)
	return NewService(pool, tokens, Policy{RefreshTTL: time.Hour, MaxFailedLogins: 3, LockoutDuration: time.Minute}), pool
}

// createUser inserts an org unit (once), an employee and a user with testPassword.
func createUser(t *testing.T, pool *pgxpool.Pool, email string, role Role) int64 {
	t.Helper()
	ctx := context.Background()
	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	var unit int64
	err = pool.QueryRow(ctx, `
		INSERT INTO org_units (code, name) VALUES ('root', 'Root')
		ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name RETURNING id`).Scan(&unit)
	if err != nil {
		t.Fatal(err)
	}
	var emp, id int64
	no := "E-" + strings.ToUpper(email[:strings.IndexByte(email, '@')])
	if err := pool.QueryRow(ctx, `
		INSERT INTO employees (employee_no, first_name, last_name, org_unit_id, hire_date)
		VALUES ($1, 'Test', 'User', $2, '2020-01-01') RETURNING id`, no, unit).Scan(&emp); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, employee_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		email, hash, role, emp).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func countAudit(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLoginSuccessAndFailures(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	id := createUser(t, pool, "ali@x.test", RoleHR)

	sess, err := svc.Login(ctx, "ALI@x.test", testPassword, Meta{IP: "10.0.0.1"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.Principal.UserID != id || sess.Principal.Role != RoleHR || sess.AccessToken == "" || sess.RefreshToken == "" {
		t.Fatalf("unexpected session %+v", sess)
	}

	if _, err := svc.Login(ctx, "ali@x.test", "wrong-password", Meta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: err = %v", err)
	}
	if _, err := svc.Login(ctx, "nobody@x.test", testPassword, Meta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown e-mail: err = %v", err)
	}

	if n := countAudit(t, pool, "auth.login"); n != 1 {
		t.Errorf("auth.login audit rows = %d, want 1", n)
	}
	if n := countAudit(t, pool, "auth.login_failed"); n != 2 {
		t.Errorf("auth.login_failed audit rows = %d, want 2", n)
	}
	var ip string
	if err := pool.QueryRow(ctx, `SELECT host(ip) FROM audit_log WHERE action = 'auth.login'`).Scan(&ip); err != nil || ip != "10.0.0.1" {
		t.Errorf("audit ip = %q, %v", ip, err)
	}
}

func TestLoginLockout(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	createUser(t, pool, "veli@x.test", RoleEmployee)

	for range 3 { // MaxFailedLogins = 3
		_, _ = svc.Login(ctx, "veli@x.test", "bad", Meta{})
	}
	// Locked: even the right password fails, with the same generic error.
	if _, err := svc.Login(ctx, "veli@x.test", testPassword, Meta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("locked account: err = %v", err)
	}

	// After the lock expires a correct login works and resets the counter.
	if _, err := pool.Exec(ctx, `UPDATE users SET locked_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "veli@x.test", testPassword, Meta{}); err != nil {
		t.Fatalf("login after lock expiry: %v", err)
	}
	var failed int
	_ = pool.QueryRow(ctx, `SELECT failed_logins FROM users`).Scan(&failed)
	if failed != 0 {
		t.Fatalf("failed_logins = %d after success, want 0", failed)
	}
}

func TestInactiveAccountCannotLoginOrRefresh(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	id := createUser(t, pool, "ayse@x.test", RoleEmployee)

	sess, err := svc.Login(ctx, "ayse@x.test", testPassword, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "ayse@x.test", testPassword, Meta{}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login inactive: %v", err)
	}
	if _, err := svc.Refresh(ctx, sess.RefreshToken, Meta{}); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("refresh inactive: %v", err)
	}
	if _, err := svc.Principal(ctx, id); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("principal inactive: %v", err)
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	createUser(t, pool, "mehmet@x.test", RolePayroll)

	s1, err := svc.Login(ctx, "mehmet@x.test", testPassword, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := svc.Refresh(ctx, s1.RefreshToken, Meta{})
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if s2.RefreshToken == s1.RefreshToken || s2.Principal.Role != RolePayroll {
		t.Fatal("refresh must rotate the token and keep the principal")
	}

	// An attacker replays the old token: the whole family dies, including s2.
	if _, err := svc.Refresh(ctx, s1.RefreshToken, Meta{}); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("replay: err = %v", err)
	}
	if _, err := svc.Refresh(ctx, s2.RefreshToken, Meta{}); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("successor after reuse: err = %v", err)
	}
	if n := countAudit(t, pool, "auth.refresh_reuse_detected"); n != 1 {
		t.Fatalf("reuse audit rows = %d, want 1", n)
	}

	if _, err := svc.Refresh(ctx, "never-issued", Meta{}); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("unknown token: err = %v", err)
	}
}

// TestConcurrentRefreshSingleWinner proves the row lock: N goroutines present
// the same refresh token at once and exactly one receives a new session.
func TestConcurrentRefreshSingleWinner(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	createUser(t, pool, "zeynep@x.test", RoleManager)

	s, err := svc.Login(ctx, "zeynep@x.test", testPassword, Meta{})
	if err != nil {
		t.Fatal(err)
	}

	const n = 10
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		rejects int
		start   = make(chan struct{})
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Refresh(ctx, s.RefreshToken, Meta{})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrInvalidRefresh):
				rejects++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 || rejects != n-1 {
		t.Fatalf("wins=%d rejects=%d, want 1 and %d", wins, rejects, n-1)
	}
	var live int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`).Scan(&live)
	if live != 0 {
		t.Fatalf("%d tokens still live; concurrent reuse must revoke the family", live)
	}
}

func TestLogoutRevokesFamily(t *testing.T) {
	svc, pool := newTestService(t)
	ctx := context.Background()
	createUser(t, pool, "fatma@x.test", RoleAccounting)

	s1, _ := svc.Login(ctx, "fatma@x.test", testPassword, Meta{})
	s2, err := svc.Refresh(ctx, s1.RefreshToken, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(ctx, s2.RefreshToken, Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, s2.RefreshToken, Meta{}); !errors.Is(err, ErrInvalidRefresh) {
		t.Fatalf("refresh after logout: err = %v", err)
	}
	// Logout is idempotent.
	if err := svc.Logout(ctx, s2.RefreshToken, Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(ctx, "", Meta{}); err != nil {
		t.Fatal(err)
	}
}
