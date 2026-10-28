package server

import (
	"net/http"
	"testing"
)

func TestPayrollPermissions(t *testing.T) {
	a := newDemoAPI(t)
	payroll, worker, mgr := a.token("bordro"), a.token("calisan"), a.token("yonetici")
	emp := id(a.empID("D0001"))

	// Only payroll specialists calculate; HR and accounting cannot.
	for _, user := range []string{"ik", "muhasebe", "yonetici", "calisan"} {
		if status, _ := a.call(http.MethodPost, "/payroll/employees/"+emp+"/calculate", a.token(user), `{"period":"2026-01"}`); status != 403 {
			t.Errorf("%s calculating payroll: %d", user, status)
		}
	}
	status, res := a.call(http.MethodPost, "/payroll/employees/"+emp+"/calculate", payroll, `{"period":"2026-01"}`)
	if status != 200 || res["net"] != "50931.18" || res["rule_version"] != "TR-2026.1" {
		t.Fatalf("calculate: %d %v", status, res)
	}

	// Drafts are not shown to the employee; payroll sees them.
	if status, body := a.call(http.MethodGet, "/payroll/employees/"+emp+"/results/2026-01", worker, ""); status != 404 || errCode(body) != "payslip_not_final" {
		t.Fatalf("own draft result: %d %v", status, body)
	}
	if status, body := a.call(http.MethodGet, "/payroll/employees/"+emp+"/results?year=2026", worker, ""); status != 200 || len(body["items"].([]any)) != 0 {
		t.Fatalf("own draft list: %d %v", status, body)
	}
	if status, body := a.call(http.MethodGet, "/payroll/employees/"+emp+"/results/2026-01", payroll, ""); status != 200 ||
		body["result"].(map[string]any)["net"] != "50931.18" {
		t.Fatalf("payroll reads draft: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodGet, "/payroll/employees/"+emp+"/results?year=2026", mgr, ""); status != 403 {
		t.Fatalf("manager reading team payroll: %d", status)
	}
	if status, _ := a.call(http.MethodGet, "/payroll/employees/"+id(a.empID("D0003"))+"/results?year=2026", worker, ""); status != 403 {
		t.Fatalf("employee reading colleague payroll: %d", status)
	}
	for _, user := range []string{"denetci", "muhasebe"} {
		if status, body := a.call(http.MethodGet, "/payroll/employees/"+emp+"/results?year=2026", a.token(user), ""); status != 200 ||
			len(body["items"].([]any)) != 1 {
			t.Errorf("%s reading payroll: %d %v", user, status, body)
		}
	}

	// Retroactive raise through the API.
	if status, _ := a.call(http.MethodPost, "/employees/"+emp+"/salaries", a.token("ik"), `{"amount":"80000.00","valid_from":"2026-01-01"}`); status != 409 {
		t.Fatalf("salary on an existing date must conflict: %d", status)
	}
	if status, _ := a.call(http.MethodPost, "/employees/"+emp+"/salaries", a.token("ik"), `{"amount":"80000.00","valid_from":"2026-01-15"}`); status != 201 {
		t.Fatalf("add raise: %d", status)
	}
	status, body := a.call(http.MethodPost, "/payroll/employees/"+emp+"/recalculate", payroll, `{"from":"2026-01"}`)
	changes, _ := body["changes"].([]any)
	if status != 200 || len(changes) != 1 || changes[0].(map[string]any)["changed"] != true {
		t.Fatalf("recalculate: %d %v", status, body)
	}

	if status, rs := a.call(http.MethodGet, "/payroll/rule-sets", payroll, ""); status != 200 || len(rs["items"].([]any)) != 2 {
		t.Fatalf("rule sets: %d %v", status, rs)
	}
	if status, _ := a.call(http.MethodPost, "/payroll/employees/"+emp+"/calculate", payroll, `{"period":"2026/01"}`); status != 422 {
		t.Fatalf("bad period: %d", status)
	}
}

// T-4 over HTTP: who may move a period, and the error codes.
func TestPayrollPeriodWorkflowAPI(t *testing.T) {
	a := newDemoAPI(t)
	payroll, accounting, hr, auditor := a.token("bordro"), a.token("muhasebe"), a.token("ik"), a.token("denetci")

	if status, _ := a.call(http.MethodPost, "/payroll/periods/2026-01/run", hr, ""); status != 403 {
		t.Fatalf("HR running payroll: %d", status)
	}
	status, rep := a.call(http.MethodPost, "/payroll/periods/2026-01/run", payroll, "")
	if status != 200 || rep["employees"] != 6.0 || len(rep["failures"].([]any)) != 0 || rep["digest"] == "" {
		t.Fatalf("run: %d %v", status, rep)
	}
	if status, _ := a.call(http.MethodPost, "/payroll/periods/2026-01/approve", payroll, ""); status != 403 {
		t.Fatalf("payroll approving: %d", status)
	}
	if status, body := a.call(http.MethodPost, "/payroll/periods/2026-01/approve", accounting, ""); status != 409 || errCode(body) != "invalid_transition" {
		t.Fatalf("approving a draft: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/payroll/periods/2026-01/submit", payroll, ""); status != 200 {
		t.Fatalf("submit: %d", status)
	}
	if status, body := a.call(http.MethodPost, "/payroll/employees/"+id(a.empID("D0001"))+"/calculate", payroll, `{"period":"2026-01"}`); status != 409 || errCode(body) != "period_frozen" {
		t.Fatalf("calculate under review: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/payroll/periods/2026-01/reject", accounting, ""); status != 422 {
		t.Fatalf("reject without a note: %d", status)
	}
	for _, step := range []string{"approve", "close"} {
		if status, body := a.call(http.MethodPost, "/payroll/periods/2026-01/"+step, accounting, ""); status != 200 {
			t.Fatalf("%s: %d %v", step, status, body)
		}
	}
	status, p := a.call(http.MethodGet, "/payroll/periods/2026-01", auditor, "")
	if status != 200 || p["status"] != "closed" || p["closed_at"] == nil {
		t.Fatalf("auditor reads period: %d %v", status, p)
	}
	if status, body := a.call(http.MethodPost, "/payroll/periods/2026-01/run", payroll, ""); status != 409 || errCode(body) != "period_frozen" {
		t.Fatalf("re-run closed period: %d %v", status, body)
	}
	if status, body := a.call(http.MethodPost, "/payroll/periods/2026-03/run", payroll, ""); status != 200 {
		t.Fatalf("March with February never opened: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodGet, "/payroll/periods", a.token("calisan"), ""); status != 403 {
		t.Fatalf("employee listing periods: %d", status)
	}
}
