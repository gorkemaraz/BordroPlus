package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"bordroplus/internal/timesheet"
)

// pastWeek is Monday 3 November 2025: a closed payroll month with no
// public holidays that week, so totals do not depend on when tests run.
func pastWeek() time.Time { return time.Date(2025, 11, 3, 0, 0, 0, 0, time.UTC) }

func stamp(d time.Time, hhmm string) string {
	return d.Format("2006-01-02") + "T" + hhmm + ":00+03:00"
}

func punchJSON(emp, at, dir string) string {
	return `{"employee_id":` + emp + `,"punched_at":"` + at + `","direction":"` + dir + `","device_id":"T1"}`
}

func TestTimesheetPermissionsAndFlow(t *testing.T) {
	a := newDemoAPI(t)
	worker, mgr, hr, auditor := a.token("calisan"), a.token("yonetici"), a.token("ik"), a.token("denetci")
	emp := id(a.empID("D0001"))
	mon := pastWeek()
	tue := mon.AddDate(0, 0, 1)
	month := timesheet.MonthOf(mon).Format("2006-01")

	batch := `{"punches":[` + strings.Join([]string{
		punchJSON(emp, stamp(mon, "08:00"), "in"), punchJSON(emp, stamp(mon, "17:00"), "out"),
		punchJSON(emp, stamp(tue, "08:00"), "in"), // Tuesday: no out
	}, ",") + `]}`
	if status, _ := a.call(http.MethodPost, "/timesheet/punches", mgr, batch); status != 403 {
		t.Fatalf("manager importing punches: %d", status)
	}
	if status, body := a.call(http.MethodPost, "/timesheet/punches", hr, batch); status != 201 || body["inserted"] != 3.0 {
		t.Fatalf("HR import: %d %v", status, body)
	}

	from, to := mon.Format("2006-01-02"), mon.AddDate(0, 0, 6).Format("2006-01-02")
	status, days := a.call(http.MethodGet, "/timesheet/employees/"+emp+"/days?from="+from+"&to="+to, worker, "")
	if status != 200 || len(days["days"].([]any)) != 1 || len(days["anomalies"].([]any)) != 1 || len(days["corrections"].([]any)) != 1 {
		t.Fatalf("days: %d %v", status, days)
	}
	if status, _ := a.call(http.MethodGet, "/timesheet/employees/"+id(a.empID("D0003"))+"/days?from="+from+"&to="+to, mgr, ""); status != 403 {
		t.Fatalf("manager reading HR staff timesheet: %d", status)
	}

	// The manager's queue shows the proposal; the employee cannot decide it.
	_, queue := a.call(http.MethodGet, "/timesheet/corrections?for_approval=true", mgr, "")
	items := queue["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("manager queue: %v", queue)
	}
	cid := id(int64(items[0].(map[string]any)["id"].(float64)))
	if status, body := a.call(http.MethodPost, "/timesheet/corrections/"+cid+"/approve", worker, ""); status != 403 || errCode(body) != "self_approval" {
		t.Fatalf("employee approving own correction: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/timesheet/corrections/"+cid+"/approve", auditor, ""); status != 403 {
		t.Fatalf("auditor approving: %d", status)
	}

	// Month approval is blocked until the proposal is decided.
	if status, body := a.call(http.MethodPost, "/timesheet/employees/"+emp+"/months/"+month+"/approve", mgr, ""); status != 409 || errCode(body) != "unresolved_anomalies" {
		t.Fatalf("approve with open anomaly: %d %v", status, body)
	}
	if status, body := a.call(http.MethodPost, "/timesheet/corrections/"+cid+"/approve", mgr,
		`{"at":"`+stamp(tue, "16:30")+`","note":"çıkış basmayı unutmuş"}`); status != 200 || body["status"] != "approved" {
		t.Fatalf("manager approves correction: %d %v", status, body)
	}
	status, sum := a.call(http.MethodPost, "/timesheet/employees/"+emp+"/months/"+month+"/approve", mgr, "")
	if status != 200 || sum["status"] != "approved" {
		t.Fatalf("approve month: %d %v", status, sum)
	}
	totals := sum["totals"].(map[string]any)
	// Mon 540-60 = 480, Tue 08:00-16:30 = 510-60 = 450.
	if totals["normal_minutes"] != 930.0 || totals["worked_days"] != 2.0 {
		t.Fatalf("totals: %v", totals)
	}
	if status, body := a.call(http.MethodPost, "/timesheet/punches", hr, `{"punches":[`+punchJSON(emp, stamp(mon, "18:00"), "in")+`]}`); status != 409 || errCode(body) != "period_locked" {
		t.Fatalf("punch into approved month: %d %v", status, body)
	}
	// Auditors read every timesheet.
	if status, m := a.call(http.MethodGet, "/timesheet/employees/"+emp+"/months/"+month, auditor, ""); status != 200 || m["status"] != "approved" {
		t.Fatalf("auditor reads month: %d %v", status, m)
	}
}

func TestWebClockAndValidation(t *testing.T) {
	a := newDemoAPI(t)
	worker, hr := a.token("calisan"), a.token("ik")

	if status, body := a.call(http.MethodPost, "/timesheet/clock", worker, `{"direction":"in"}`); status != 201 || body["direction"] != "in" {
		t.Fatalf("clock in: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/timesheet/clock", worker, `{"direction":"sideways"}`); status != 422 {
		t.Fatalf("bad direction: %d", status)
	}
	future := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	cases := map[string]string{
		"future punch":   `{"punches":[` + punchJSON("1", future, "in") + `]}`,
		"bad timestamp":  `{"punches":[` + punchJSON("1", "2026-11-02 08:00", "in") + `]}`,
		"empty batch":    `{"punches":[]}`,
		"unknown worker": `{"punches":[` + punchJSON("99999", stamp(pastWeek(), "08:00"), "in") + `]}`,
	}
	for name, body := range cases {
		if status, resp := a.call(http.MethodPost, "/timesheet/punches", hr, body); status != 422 {
			t.Errorf("%s: %d %v", name, status, resp)
		}
	}
	if status, _ := a.call(http.MethodGet, "/timesheet/employees/1/days?from=2026-01-01&to=2026-06-01", hr, ""); status != 422 {
		t.Errorf("range too long: %d", status)
	}
	if status, _ := a.call(http.MethodGet, "/timesheet/employees/1/months/2026-13", hr, ""); status != 422 {
		t.Errorf("bad month: %d", status)
	}
}
