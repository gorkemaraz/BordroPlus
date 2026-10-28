package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bordroplus/internal/auth"
	"bordroplus/internal/config"
	"bordroplus/internal/testdb"
)

const apiPassword = "Api.Test.Parola1"

// newAPI starts the full router on a migrated test database with one user per
// role, e-mail "<role>@x.test".
func newAPI(t *testing.T) *httptest.Server {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()

	hash, err := auth.HashPassword(apiPassword)
	if err != nil {
		t.Fatal(err)
	}
	var unit int64
	if err := pool.QueryRow(ctx, `INSERT INTO org_units (code, name) VALUES ('root', 'Root') RETURNING id`).Scan(&unit); err != nil {
		t.Fatal(err)
	}
	for _, role := range auth.Roles {
		var emp int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO employees (employee_no, first_name, last_name, org_unit_id, hire_date)
			VALUES ($1, 'Test', $2, $3, '2020-01-01') RETURNING id`,
			"E-"+strings.ToUpper(string(role)), string(role), unit).Scan(&emp); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash, role, employee_id) VALUES ($1, $2, $3, $4)`,
			string(role)+"@x.test", hash, role, emp); err != nil {
			t.Fatal(err)
		}
	}

	h := New(Deps{
		Config: config.Config{
			RequestTimeout:  10 * time.Second,
			JWTSecret:       []byte(strings.Repeat("k", 32)),
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: time.Hour,
			MaxFailedLogins: 5,
			LockoutDuration: time.Minute,
		},
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), DB: pool, Pool: pool, Box: testBox(t), Version: "test",
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

func do(t *testing.T, c *http.Client, method, url, token, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func login(t *testing.T, srv *httptest.Server, c *http.Client, role auth.Role) string {
	t.Helper()
	status, body := do(t, c, http.MethodPost, srv.URL+"/api/v1/auth/login", "",
		`{"email":"`+string(role)+`@x.test","password":"`+apiPassword+`"}`)
	if status != http.StatusOK {
		t.Fatalf("login %s: status %d body %v", role, status, body)
	}
	return body["access_token"].(string)
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestAPILoginAndMe(t *testing.T) {
	srv := newAPI(t)
	c := newClient(t)
	token := login(t, srv, c, auth.RoleManager)

	status, me := do(t, c, http.MethodGet, srv.URL+"/api/v1/auth/me", token, "")
	if status != http.StatusOK || me["role"] != "manager" || me["email"] != "manager@x.test" {
		t.Fatalf("me: %d %v", status, me)
	}
	perms, _ := me["permissions"].([]any)
	if len(perms) != len(auth.RoleManager.Permissions()) {
		t.Fatalf("permissions = %v", perms)
	}
}

func TestAPIAuthErrors(t *testing.T) {
	srv := newAPI(t)
	c := newClient(t)

	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"wrong password", `{"email":"hr@x.test","password":"nope"}`, 401, "invalid_credentials"},
		{"unknown user", `{"email":"ghost@x.test","password":"nope"}`, 401, "invalid_credentials"},
		{"bad e-mail", `{"email":"ghost","password":"x"}`, 422, "validation_failed"},
		{"unknown field", `{"email":"hr@x.test","password":"x","role":"hr"}`, 400, "invalid_json"},
		{"not json", `email=hr`, 400, "invalid_json"},
	}
	for _, tc := range cases {
		status, body := do(t, c, http.MethodPost, srv.URL+"/api/v1/auth/login", "", tc.body)
		if status != tc.status || errCode(body) != tc.code {
			t.Errorf("%s: got %d %q, want %d %q", tc.name, status, errCode(body), tc.status, tc.code)
		}
	}

	for name, token := range map[string]string{"no token": "", "garbage token": "abc.def.ghi"} {
		status, body := do(t, c, http.MethodGet, srv.URL+"/api/v1/auth/me", token, "")
		if status != 401 || errCode(body) != "unauthorized" {
			t.Errorf("%s: got %d %v", name, status, body)
		}
	}
}

// TestAPIAuditLogRBAC checks the server-side permission gate: only the
// auditor may read the audit log; every other role gets 403.
func TestAPIAuditLogRBAC(t *testing.T) {
	srv := newAPI(t)

	for _, role := range auth.Roles {
		c := newClient(t)
		token := login(t, srv, c, role)
		status, body := do(t, c, http.MethodGet, srv.URL+"/api/v1/audit-log?limit=5", token, "")
		want := http.StatusForbidden
		if role == auth.RoleAuditor {
			want = http.StatusOK
		}
		if status != want {
			t.Errorf("%s: status %d, want %d (%v)", role, status, want, body)
		}
		if want == http.StatusForbidden && errCode(body) != "forbidden" {
			t.Errorf("%s: error code %q", role, errCode(body))
		}
	}
}

func TestAPIAuditLogKeysetPagination(t *testing.T) {
	srv := newAPI(t)
	c := newClient(t)
	token := login(t, srv, c, auth.RoleAuditor) // fresh DB: this login is audit row 1

	// Produce a few more audit rows.
	for range 4 {
		do(t, newClient(t), http.MethodPost, srv.URL+"/api/v1/auth/login", "", `{"email":"hr@x.test","password":"bad"}`)
	}

	seen := map[float64]bool{}
	url := srv.URL + "/api/v1/audit-log?limit=2"
	pages := 0
	for {
		status, body := do(t, c, http.MethodGet, url, token, "")
		if status != 200 {
			t.Fatalf("status %d %v", status, body)
		}
		pages++
		for _, it := range body["items"].([]any) {
			id := it.(map[string]any)["id"].(float64)
			if seen[id] {
				t.Fatalf("row %v returned twice", id)
			}
			seen[id] = true
		}
		next, _ := body["next_cursor"].(string)
		if next == "" {
			break
		}
		url = srv.URL + "/api/v1/audit-log?limit=2&cursor=" + next
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("saw %d rows over %d pages, want 5 rows over 3 pages", len(seen), pages)
	}

	status, body := do(t, c, http.MethodGet, srv.URL+"/api/v1/audit-log?action=auth.login_failed", token, "")
	if status != 200 || len(body["items"].([]any)) != 4 {
		t.Fatalf("action filter: %d %v", status, body)
	}
	if status, _ := do(t, c, http.MethodGet, srv.URL+"/api/v1/audit-log?limit=999", token, ""); status != 422 {
		t.Fatalf("limit over max: status %d", status)
	}
}

func TestAPIRefreshCookieFlow(t *testing.T) {
	srv := newAPI(t)
	c := newClient(t)
	login(t, srv, c, auth.RoleEmployee)

	// The cookie jar sends bp_refresh only to /api/v1/auth/*.
	status, body := do(t, c, http.MethodPost, srv.URL+"/api/v1/auth/refresh", "", "")
	if status != 200 || body["access_token"] == "" {
		t.Fatalf("refresh: %d %v", status, body)
	}
	token := body["access_token"].(string)
	if status, _ := do(t, c, http.MethodGet, srv.URL+"/api/v1/auth/me", token, ""); status != 200 {
		t.Fatalf("me with refreshed token: %d", status)
	}

	if status, _ := do(t, c, http.MethodPost, srv.URL+"/api/v1/auth/logout", "", ""); status != 204 {
		t.Fatalf("logout: %d", status)
	}
	status, body = do(t, c, http.MethodPost, srv.URL+"/api/v1/auth/refresh", "", "")
	if status != 401 || errCode(body) != "invalid_refresh_token" {
		t.Fatalf("refresh after logout: %d %v", status, body)
	}
}

func TestAPIRefreshCookieAttributes(t *testing.T) {
	srv := newAPI(t)
	resp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"hr@x.test","password":"`+apiPassword+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var ck *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.RefreshCookie {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/api/v1/auth" {
		t.Fatalf("refresh cookie attributes wrong: %+v", ck)
	}
}
