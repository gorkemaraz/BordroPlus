package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func get(t *testing.T, a *demoAPI, path, token string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, a.srv.URL+"/api/v1"+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestPayrollItemsAndPayslipAPI(t *testing.T) {
	a := newDemoAPI(t)
	payroll, worker, accounting := a.token("bordro"), a.token("calisan"), a.token("muhasebe")
	emp := id(a.empID("D0001"))

	item := `{"employee_id":` + emp + `,"kind":"loan","label":"Eğitim borcu","amount":"9000.00","instalments":3,"start_period":"2026-01"}`
	if status, _ := a.call(http.MethodPost, "/payroll/items", a.token("ik"), item); status != 403 {
		t.Fatalf("HR adding payroll items: %d", status)
	}
	status, it := a.call(http.MethodPost, "/payroll/items", payroll, item)
	if status != 201 || len(it["schedule"].([]any)) != 3 {
		t.Fatalf("create loan: %d %v", status, it)
	}
	bad := map[string]string{
		"float amount":       `{"employee_id":` + emp + `,"kind":"bonus","label":"x","amount":"1.005","start_period":"2026-01"}`,
		"split advance":      `{"employee_id":` + emp + `,"kind":"advance","label":"x","amount":"100","instalments":2,"start_period":"2026-01"}`,
		"over deduction cap": `{"employee_id":` + emp + `,"kind":"advance","label":"x","amount":"20000","start_period":"2026-01"}`,
	}
	for name, body := range bad {
		if status, resp := a.call(http.MethodPost, "/payroll/items", payroll, body); status != 422 {
			t.Errorf("%s: %d %v", name, status, resp)
		}
	}

	a.call(http.MethodPost, "/payroll/periods/2026-01/run", payroll, "")
	// Draft: the employee does not see a payslip yet; payroll does.
	if status, body := a.call(http.MethodGet, "/payroll/employees/"+emp+"/payslips/2026-01", worker, ""); status != 404 || errCode(body) != "payslip_not_final" {
		t.Fatalf("employee payslip of a draft: %d %v", status, body)
	}
	a.call(http.MethodPost, "/payroll/periods/2026-01/submit", payroll, "")
	a.call(http.MethodPost, "/payroll/periods/2026-01/approve", accounting, "")

	status, slip := a.call(http.MethodGet, "/payroll/employees/"+emp+"/payslips/2026-01", worker, "")
	if status != 200 || slip["final"] != true || slip["net"] != "47931.18" { // 50.931,18 - 3.000 instalment
		t.Fatalf("final payslip: %d %v", status, slip)
	}
	resp, html := get(t, a, "/payroll/employees/"+emp+"/payslips/2026-01?format=html", worker)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") ||
		!strings.Contains(html, "Eğitim borcu (1/3)") || !strings.Contains(html, "Can Demir") {
		t.Fatalf("payslip html: %d %s", resp.StatusCode, html)
	}
	if status, _ := a.call(http.MethodGet, "/payroll/employees/"+id(a.empID("D0003"))+"/payslips/2026-01", worker, ""); status != 403 {
		t.Fatalf("someone else's payslip: %d", status)
	}
	// The period is approved: its items are frozen.
	if status, body := a.call(http.MethodPost, "/payroll/items/"+id(int64(it["id"].(float64)))+"/cancel", payroll, ""); status != 409 || errCode(body) != "period_frozen" {
		t.Fatalf("cancel item of an approved period: %d %v", status, body)
	}
}

func TestReportsAPI(t *testing.T) {
	a := newDemoAPI(t)
	payroll, auditor := a.token("bordro"), a.token("denetci")
	_, rep := a.call(http.MethodPost, "/payroll/periods/2026-01/run", payroll, "")

	if status, _ := a.call(http.MethodGet, "/reports/payroll-summary?period=2026-01", a.token("yonetici"), ""); status != 403 {
		t.Fatalf("manager reading reports: %d", status)
	}
	status, sum := a.call(http.MethodGet, "/reports/payroll-summary?period=2026-01&level=2", auditor, "")
	if status != 200 || sum["period_status"] != "draft" {
		t.Fatalf("summary: %d %v", status, sum)
	}
	total := sum["total"].(map[string]any)
	totals := rep["totals"].(map[string]any)
	if total["headcount"] != 6.0 || total["net"] != totals["net"] || total["employer_cost"] != totals["employer_cost"] {
		t.Fatalf("summary total %v does not match the run %v", total, totals)
	}
	units := map[string]bool{}
	for _, it := range sum["items"].([]any) {
		units[it.(map[string]any)["unit_name"].(string)] = true
	}
	if !units["Bilgi Teknolojileri"] || !units["Finans"] || len(units) != 4 {
		t.Fatalf("units at level 2: %v", units)
	}

	resp, csv := get(t, a, "/reports/payroll-summary?period=2026-01&format=csv", auditor)
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if resp.StatusCode != 200 || !strings.Contains(lines[0], "Birim;Yol;Kişi;Brüt") || !strings.HasPrefix(lines[len(lines)-1], "Toplam;;6;") {
		t.Fatalf("csv: %d\n%s", resp.StatusCode, csv)
	}

	status, cost := a.call(http.MethodGet, "/reports/employer-cost?year=2026", auditor, "")
	if status != 200 || len(cost["items"].([]any)) != 1 || cost["total"].(map[string]any)["employer_cost"] != totals["employer_cost"] {
		t.Fatalf("employer cost: %d %v", status, cost)
	}

	status, lv := a.call(http.MethodGet, "/reports/leave-usage?level=1", a.token("ik"), "")
	items := lv["items"].([]any)
	if status != 200 || len(items) != 1 || items[0].(map[string]any)["headcount"] != 6.0 || items[0].(map[string]any)["accrued_days"].(float64) <= 0 {
		t.Fatalf("leave usage: %d %v", status, lv)
	}
	if status, ot := a.call(http.MethodGet, "/reports/overtime?month=2026-01", auditor, ""); status != 200 || ot["over_annual_cap"] == nil {
		t.Fatalf("overtime: %d %v", status, ot)
	}
	if status, _ := a.call(http.MethodGet, "/reports/payroll-summary", auditor, ""); status != 422 {
		t.Fatalf("missing period: %d", status)
	}
}
