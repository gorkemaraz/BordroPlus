package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/config"
	"bordroplus/internal/secret"
	"bordroplus/internal/seed"
	"bordroplus/internal/testdb"
)

const demoPassword = "Demo.Test.2026"

func testBox(t *testing.T) *secret.Box {
	t.Helper()
	b, err := secret.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// demoAPI runs the router on a database seeded exactly like production:
//
//	genel_mudurluk
//	├── bilgi_teknolojileri   manager: yonetici (D0002)
//	│   └── yazilim           calisan (D0001)
//	├── insan_kaynaklari      ik (D0003)
//	├── finans ─ bordro (D0004), muhasebe (D0005)
//	└── ic_denetim            denetci (D0006)
type demoAPI struct {
	t    *testing.T
	srv  *httptest.Server
	pool *pgxpool.Pool
}

func newDemoAPI(t *testing.T) *demoAPI {
	t.Helper()
	pool := testdb.New(t)
	box := testBox(t)
	if _, err := seed.Demo(context.Background(), pool, box, demoPassword); err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		Config: config.Config{
			RequestTimeout: 10 * time.Second, JWTSecret: []byte(strings.Repeat("k", 32)),
			AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour,
			MaxFailedLogins: 5, LockoutDuration: time.Minute,
		},
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), DB: pool, Pool: pool, Box: box, Version: "test",
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &demoAPI{t: t, srv: srv, pool: pool}
}

func (a *demoAPI) token(user string) string {
	a.t.Helper()
	status, body := do(a.t, http.DefaultClient, http.MethodPost, a.srv.URL+"/api/v1/auth/login", "",
		`{"email":"`+user+`@bordroplus.test","password":"`+demoPassword+`"}`)
	if status != http.StatusOK {
		a.t.Fatalf("login %s: %d %v", user, status, body)
	}
	return body["access_token"].(string)
}

func (a *demoAPI) call(method, path, token, body string) (int, map[string]any) {
	a.t.Helper()
	return do(a.t, http.DefaultClient, method, a.srv.URL+"/api/v1"+path, token, body)
}

func (a *demoAPI) empID(no string) int64 {
	a.t.Helper()
	var id int64
	if err := a.pool.QueryRow(context.Background(), `SELECT id FROM employees WHERE employee_no = $1`, no).Scan(&id); err != nil {
		a.t.Fatal(err)
	}
	return id
}

func (a *demoAPI) unitID(code string) int64 {
	a.t.Helper()
	var id int64
	if err := a.pool.QueryRow(context.Background(), `SELECT id FROM org_units WHERE code = $1`, code).Scan(&id); err != nil {
		a.t.Fatal(err)
	}
	return id
}

func employeeNos(body map[string]any) []string {
	var out []string
	items, _ := body["items"].([]any)
	for _, it := range items {
		out = append(out, it.(map[string]any)["employee_no"].(string))
	}
	return out
}

func id(n int64) string { return strconv.FormatInt(n, 10) }

// T-5: each role sees exactly its scope of the employee list.
func TestEmployeeListScopes(t *testing.T) {
	a := newDemoAPI(t)
	cases := map[string]string{
		"calisan":  "D0001",                               // self only
		"yonetici": "D0001,D0002",                         // IT sub-tree
		"ik":       "D0001,D0002,D0003,D0004,D0005,D0006", // everyone
		"denetci":  "D0001,D0002,D0003,D0004,D0005,D0006",
	}
	for user, want := range cases {
		status, body := a.call(http.MethodGet, "/employees", a.token(user), "")
		if got := strings.Join(employeeNos(body), ","); status != 200 || got != want {
			t.Errorf("%s: %d %q, want %q", user, status, got, want)
		}
	}
}

// T-5: reading an employee outside the scope is a 403; salaries are visible
// only to salary.read holders and the employee themself.
func TestEmployeeCardAccessAndSalaryMasking(t *testing.T) {
	a := newDemoAPI(t)
	worker, hr := id(a.empID("D0001")), id(a.empID("D0003"))

	type want struct {
		status int
		salary string // "" = masked
	}
	cases := []struct {
		user, emp string
		want
	}{
		{"calisan", worker, want{200, "65000.00"}}, // own card, own salary
		{"calisan", hr, want{403, ""}},
		{"yonetici", worker, want{200, ""}}, // team member, salary masked
		{"yonetici", hr, want{403, ""}},
		{"ik", worker, want{200, "65000.00"}},
		{"bordro", worker, want{200, "65000.00"}},
		{"denetci", worker, want{200, ""}}, // auditor: card yes, salary no
		{"muhasebe", worker, want{200, ""}},
	}
	for _, c := range cases {
		status, body := a.call(http.MethodGet, "/employees/"+c.emp, a.token(c.user), "")
		if status != c.status {
			t.Errorf("%s -> %s: status %d, want %d", c.user, c.emp, status, c.status)
			continue
		}
		if status != 200 {
			continue
		}
		sal, _ := body["salary"].(map[string]any)
		switch {
		case c.salary == "" && (sal != nil || body["salary_masked"] != true):
			t.Errorf("%s -> %s: salary must be masked, got %v", c.user, c.emp, body)
		case c.salary != "" && (sal == nil || sal["amount"] != c.salary):
			t.Errorf("%s -> %s: salary = %v, want %s", c.user, c.emp, sal, c.salary)
		}
	}

	if status, _ := a.call(http.MethodGet, "/employees/999999", a.token("ik"), ""); status != 404 {
		t.Errorf("unknown employee: %d, want 404", status)
	}
	if status, _ := a.call(http.MethodGet, "/employees/"+worker+"/salaries", a.token("denetci"), ""); status != 403 {
		t.Errorf("auditor salary history: %d, want 403", status)
	}
}

// Salaries are encrypted at rest: the stored bytes do not contain the amount.
func TestSalaryEncryptedAtRest(t *testing.T) {
	a := newDemoAPI(t)
	var enc []byte
	if err := a.pool.QueryRow(context.Background(),
		`SELECT amount_enc FROM employee_salaries WHERE employee_id = $1`, a.empID("D0001")).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	plain := []byte{0, 0, 0, 0, 0, 0x63, 0x2e, 0xa0} // 6,500,000 kuruş big-endian
	if strings.Contains(string(enc), string(plain)) || strings.Contains(string(enc), "6500000") {
		t.Fatal("salary stored in clear")
	}
}

// T-5: a manager on leave delegates to a team member, who then sees the
// manager's sub-tree until the delegation is revoked.
func TestDelegationGrantsAndRevokesTeamScope(t *testing.T) {
	a := newDemoAPI(t)
	mgrTok, workerTok := a.token("yonetici"), a.token("calisan")
	worker := a.empID("D0001")
	today := time.Now().Format("2006-01-02")
	nextWeek := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	status, d := a.call(http.MethodPost, "/delegations", mgrTok,
		`{"delegate_employee_id":`+id(worker)+`,"start_date":"`+today+`","end_date":"`+nextWeek+`","reason":"yıllık izin"}`)
	if status != 201 || d["active"] != true {
		t.Fatalf("create delegation: %d %v", status, d)
	}

	// The delegate's token is unchanged, but scope is evaluated per request.
	_, me := a.call(http.MethodGet, "/auth/me", workerTok, "")
	if perms := me["permissions"].([]any); !containsAny(perms, "leave.approve.team") {
		t.Fatalf("delegate lacks team permissions: %v", perms)
	}
	_, body := a.call(http.MethodGet, "/employees", workerTok, "")
	if got := strings.Join(employeeNos(body), ","); got != "D0001,D0002" {
		t.Fatalf("delegate sees %q, want the IT sub-tree", got)
	}

	// Overlapping delegation for the same manager is rejected.
	status, body = a.call(http.MethodPost, "/delegations", mgrTok,
		`{"delegate_employee_id":`+id(a.empID("D0003"))+`,"start_date":"`+nextWeek+`","end_date":"`+nextWeek+`"}`)
	if status != 409 || errCode(body) != "delegation_overlap" {
		t.Fatalf("overlap: %d %v", status, body)
	}

	// The delegate cannot re-delegate, and a plain employee cannot revoke.
	if status, _ := a.call(http.MethodPost, "/delegations", workerTok,
		`{"delegate_employee_id":`+id(a.empID("D0003"))+`,"start_date":"`+today+`","end_date":"`+today+`"}`); status != 403 {
		t.Fatalf("re-delegation: %d, want 403", status)
	}
	did := id(int64(d["id"].(float64)))
	if status, _ := a.call(http.MethodPost, "/delegations/"+did+"/revoke", workerTok, ""); status != 403 {
		t.Fatalf("revoke by delegate: %d, want 403", status)
	}

	if status, body := a.call(http.MethodPost, "/delegations/"+did+"/revoke", mgrTok, ""); status != 200 || body["active"] != false {
		t.Fatalf("revoke: %d %v", status, body)
	}
	_, body = a.call(http.MethodGet, "/employees", workerTok, "")
	if got := strings.Join(employeeNos(body), ","); got != "D0001" {
		t.Fatalf("after revoke delegate sees %q, want self only", got)
	}
	if status, _ := a.call(http.MethodPost, "/delegations/"+did+"/revoke", mgrTok, ""); status != 409 {
		t.Fatalf("second revoke: %d, want 409", status)
	}
}

func containsAny(list []any, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestDelegationRules(t *testing.T) {
	a := newDemoAPI(t)
	today := time.Now().Format("2006-01-02")
	// The server's "today" is the Europe/Istanbul date; the test clock may be
	// UTC (behind by up to 3 h), so a delegation that must be valid ends a few
	// days ahead rather than today, to clear the server's end-not-in-the-past check.
	future := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	body := func(mgr, delegate int64, start, end string) string {
		m := ""
		if mgr > 0 {
			m = `"manager_employee_id":` + id(mgr) + `,`
		}
		return `{` + m + `"delegate_employee_id":` + id(delegate) + `,"start_date":"` + start + `","end_date":"` + end + `"}`
	}
	mgr, worker, hr := a.empID("D0002"), a.empID("D0001"), a.empID("D0003")

	cases := []struct {
		name, user, body string
		status           int
	}{
		{"employee has no delegation permission", "calisan", body(0, hr, today, today), 403},
		{"auditor has no delegation permission", "denetci", body(mgr, worker, today, today), 403},
		{"manager cannot delegate someone else's scope", "yonetici", body(a.empID("D0003"), worker, today, today), 403},
		{"self delegation", "yonetici", body(0, mgr, today, today), 422},
		{"end before start", "yonetici", body(0, worker, "2030-01-10", "2030-01-01"), 422},
		{"already over", "yonetici", body(0, worker, "2020-01-01", "2020-01-02"), 422},
		{"hr for a non-manager", "ik", body(worker, hr, today, future), 422},
		{"hr for the IT manager", "ik", body(mgr, hr, today, future), 201},
	}
	for _, c := range cases {
		if status, resp := a.call(http.MethodPost, "/delegations", a.token(c.user), c.body); status != c.status {
			t.Errorf("%s: %d %v, want %d", c.name, status, resp, c.status)
		}
	}
}

// Overlapping delegations sent concurrently: the exclusion constraint lets
// exactly one through, without any application-level lock.
func TestConcurrentOverlappingDelegations(t *testing.T) {
	a := newDemoAPI(t)
	tok := a.token("ik")
	mgr := a.empID("D0002")
	delegates := []int64{a.empID("D0001"), a.empID("D0003"), a.empID("D0004"), a.empID("D0005"), a.empID("D0006")}
	start := time.Now().AddDate(0, 1, 0)

	var wg sync.WaitGroup
	results := make(chan int, len(delegates))
	for i, d := range delegates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each range overlaps all others on day start+4.
			s := start.AddDate(0, 0, i).Format("2006-01-02")
			e := start.AddDate(0, 0, 4+i).Format("2006-01-02")
			status, _ := a.call(http.MethodPost, "/delegations", tok,
				`{"manager_employee_id":`+id(mgr)+`,"delegate_employee_id":`+id(d)+`,"start_date":"`+s+`","end_date":"`+e+`"}`)
			results <- status
		}()
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for s := range results {
		counts[s]++
	}
	if counts[201] != 1 || counts[409] != len(delegates)-1 {
		t.Fatalf("statuses = %v, want one 201 and %d 409", counts, len(delegates)-1)
	}
}

// Moving a unit moves its whole sub-tree in or out of a manager's scope.
func TestUnitMoveChangesManagerScope(t *testing.T) {
	a := newDemoAPI(t)
	hrTok, mgrTok := a.token("ik"), a.token("yonetici")
	software, it, hrUnit := id(a.unitID("yazilim")), id(a.unitID("bilgi_teknolojileri")), id(a.unitID("insan_kaynaklari"))

	status, body := a.call(http.MethodPatch, "/org/units/"+software, hrTok, `{"parent_id":`+hrUnit+`}`)
	if status != 200 || body["path"] != "genel_mudurluk.insan_kaynaklari.yazilim" {
		t.Fatalf("move: %d %v", status, body)
	}
	_, body = a.call(http.MethodGet, "/employees", mgrTok, "")
	if got := strings.Join(employeeNos(body), ","); got != "D0002" {
		t.Fatalf("after move manager sees %q, want only themself", got)
	}

	// A unit cannot move under its own descendant; only HR may restructure.
	if status, body := a.call(http.MethodPatch, "/org/units/"+hrUnit, hrTok, `{"parent_id":`+software+`}`); status != 422 {
		t.Fatalf("cycle: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPatch, "/org/units/"+it, mgrTok, `{"name":"BT"}`); status != 403 {
		t.Fatalf("manager restructuring: %d, want 403", status)
	}

	status, body = a.call(http.MethodGet, "/org/units", mgrTok, "")
	if items, _ := body["items"].([]any); status != 200 || len(items) != 8 {
		t.Fatalf("org tree: %d %v", status, body)
	}
}

func TestCreateEmployeeIdempotent(t *testing.T) {
	a := newDemoAPI(t)
	tok := a.token("ik")
	payload := `{"employee_no":"E-9001","first_name":"Mert","last_name":"Yıldız","title":"Analist",
		"org_unit_id":` + id(a.unitID("yazilim")) + `,"hire_date":"2026-02-01",
		"salary":{"amount":"72000.50","valid_from":"2026-02-01"}}`

	send := func(key, body string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/employees", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}

	r1, b1 := send("create-mert", payload)
	r2, b2 := send("create-mert", payload)
	if r1.StatusCode != 201 || r2.StatusCode != 201 || b1["id"] != b2["id"] {
		t.Fatalf("replay: %d %v / %d %v", r1.StatusCode, b1, r2.StatusCode, b2)
	}
	if r2.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("replayed response not marked")
	}
	var n int
	_ = a.pool.QueryRow(context.Background(), `SELECT count(*) FROM employees WHERE employee_no = 'E-9001'`).Scan(&n)
	if n != 1 {
		t.Fatalf("employee rows = %d, want 1", n)
	}

	if r, b := send("create-mert", strings.Replace(payload, "Mert", "Mehmet", 1)); r.StatusCode != 422 || errCode(b) != "idempotency_key_reused" {
		t.Fatalf("key reuse with another body: %d %v", r.StatusCode, b)
	}
	if r, b := send("create-mert-2", payload); r.StatusCode != 409 || errCode(b) != "employee_no_taken" {
		t.Fatalf("duplicate employee_no: %d %v", r.StatusCode, b)
	}

	// The audit trail records the creation but never the amount.
	var newValue string
	_ = a.pool.QueryRow(context.Background(),
		`SELECT new_value::text FROM audit_log WHERE action = 'employee.create'`).Scan(&newValue)
	if !strings.Contains(newValue, `"salary": "set"`) || strings.Contains(newValue, "72000") {
		t.Fatalf("audit new_value = %s", newValue)
	}

	_, hist := a.call(http.MethodGet, "/employees/"+id(int64(b1["id"].(float64)))+"/salaries", a.token("bordro"), "")
	if items := hist["items"].([]any); len(items) != 1 || items[0].(map[string]any)["amount"] != "72000.50" {
		t.Fatalf("salary history: %v", hist)
	}
}

func TestEmployeeWriteValidationAndPermissions(t *testing.T) {
	a := newDemoAPI(t)
	unit := id(a.unitID("yazilim"))
	cases := []struct {
		name, user, body string
		status           int
	}{
		{"manager cannot create", "yonetici", `{"employee_no":"E-1","first_name":"A","last_name":"B","org_unit_id":` + unit + `,"hire_date":"2026-01-01"}`, 403},
		{"payroll cannot create", "bordro", `{"employee_no":"E-100","first_name":"A","last_name":"B","org_unit_id":` + unit + `,"hire_date":"2026-01-01"}`, 403},
		{"bad fields", "ik", `{"employee_no":"x","first_name":"","org_unit_id":` + unit + `,"hire_date":"01.01.2026"}`, 422},
		{"float salary", "ik", `{"employee_no":"E-101","first_name":"A","last_name":"B","org_unit_id":` + unit + `,"hire_date":"2026-01-01","salary":{"amount":"1.005","valid_from":"2026-01-01"}}`, 422},
		{"unknown unit", "ik", `{"employee_no":"E-102","first_name":"A","last_name":"B","org_unit_id":99999,"hire_date":"2026-01-01"}`, 422},
	}
	for _, c := range cases {
		if status, body := a.call(http.MethodPost, "/employees", a.token(c.user), c.body); status != c.status {
			t.Errorf("%s: %d %v, want %d", c.name, status, body, c.status)
		}
	}

	worker := id(a.empID("D0001"))
	if status, body := a.call(http.MethodPatch, "/employees/"+worker, a.token("ik"), `{"termination_date":"2000-01-01"}`); status != 422 {
		t.Errorf("termination before hire: %d %v", status, body)
	}
	if status, body := a.call(http.MethodPatch, "/employees/"+worker, a.token("ik"), `{"title":"Kıdemli Geliştirici"}`); status != 200 || body["title"] != "Kıdemli Geliştirici" {
		t.Errorf("update title: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/employees/"+worker+"/salaries", a.token("ik"), `{"amount":"70000","valid_from":"2026-01-01"}`); status != 409 {
		t.Errorf("salary for an existing date: %d, want 409", status)
	}
	if status, _ := a.call(http.MethodPost, "/employees/"+worker+"/salaries", a.token("muhasebe"), `{"amount":"70000","valid_from":"2026-07-01"}`); status != 403 {
		t.Errorf("accounting setting salary: %d, want 403", status)
	}
}
