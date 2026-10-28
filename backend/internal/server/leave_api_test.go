package server

import (
	"net/http"
	"testing"
	"time"
)

// futureWeek returns Monday and Wednesday of a week at least `weeks` weeks
// ahead, as YYYY-MM-DD.
func futureWeek(weeks int) (string, string) {
	d := time.Now().AddDate(0, 0, 7*weeks)
	for d.Weekday() != time.Monday {
		d = d.AddDate(0, 0, 1)
	}
	return d.Format("2006-01-02"), d.AddDate(0, 0, 2).Format("2006-01-02")
}

func leaveBody(start, end string) string {
	return `{"leave_type":"annual","start_date":"` + start + `","end_date":"` + end + `"}`
}

func TestLeaveRequestApprovalPermissions(t *testing.T) {
	a := newDemoAPI(t)
	worker, mgr, hr := a.token("calisan"), a.token("yonetici"), a.token("ik")
	start, end := futureWeek(4)

	status, req := a.call(http.MethodPost, "/leave/requests", worker, leaveBody(start, end))
	if status != 201 || req["status"] != "pending" {
		t.Fatalf("employee request: %d %v", status, req)
	}
	rid := id(int64(req["id"].(float64)))

	// The manager sees it among the requests waiting for them.
	_, list := a.call(http.MethodGet, "/leave/requests?for_approval=true", mgr, "")
	if items := list["items"].([]any); len(items) != 1 {
		t.Fatalf("manager approval queue: %v", list)
	}
	// The employee cannot approve their own request; the auditor cannot approve at all.
	if status, body := a.call(http.MethodPost, "/leave/requests/"+rid+"/approve", worker, ""); status != 403 {
		t.Fatalf("self approval: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/leave/requests/"+rid+"/approve", a.token("denetci"), ""); status != 403 {
		t.Fatalf("auditor approval: %d", status)
	}
	if status, body := a.call(http.MethodPost, "/leave/requests/"+rid+"/approve", mgr, `{"note":"iyi tatiller"}`); status != 200 || body["status"] != "approved" {
		t.Fatalf("manager approval: %d %v", status, body)
	}
	// Once approved, the employee can no longer withdraw it; HR can.
	if status, _ := a.call(http.MethodPost, "/leave/requests/"+rid+"/cancel", worker, ""); status != 409 {
		t.Fatalf("employee cancelling approved leave: %d, want 409", status)
	}
	if status, _ := a.call(http.MethodPost, "/leave/requests/"+rid+"/cancel", hr, ""); status != 200 {
		t.Fatalf("HR cancel: %d", status)
	}

	// A manager's own leave goes to HR, not to themself.
	status, own := a.call(http.MethodPost, "/leave/requests", mgr, leaveBody(start, end))
	if status != 201 {
		t.Fatalf("manager request: %d %v", status, own)
	}
	oid := id(int64(own["id"].(float64)))
	if status, body := a.call(http.MethodPost, "/leave/requests/"+oid+"/approve", mgr, ""); status != 403 || errCode(body) != "self_approval" {
		t.Fatalf("manager self approval: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/leave/requests/"+oid+"/approve", hr, ""); status != 200 {
		t.Fatalf("HR approves manager leave: %d", status)
	}

	// The manager cannot decide on HR staff (outside the sub-tree).
	status, hrReq := a.call(http.MethodPost, "/leave/requests", hr, leaveBody(start, end))
	if status != 201 {
		t.Fatalf("hr request: %d %v", status, hrReq)
	}
	if status, _ := a.call(http.MethodPost, "/leave/requests/"+id(int64(hrReq["id"].(float64)))+"/approve", mgr, ""); status != 403 {
		t.Fatalf("manager approving outside sub-tree: %d", status)
	}
}

func TestLeaveVisibilityAndValidation(t *testing.T) {
	a := newDemoAPI(t)
	worker, mgr := a.token("calisan"), a.token("yonetici")
	workerID, hrID := id(a.empID("D0001")), id(a.empID("D0003"))

	// Balance: self and manager yes, manager for HR staff no.
	if status, b := a.call(http.MethodGet, "/leave/employees/"+workerID+"/balance", worker, ""); status != 200 || b["service_years"] == nil {
		t.Fatalf("own balance: %d %v", status, b)
	}
	if status, _ := a.call(http.MethodGet, "/leave/employees/"+workerID+"/balance", mgr, ""); status != 200 {
		t.Fatalf("manager reads team balance: %d", status)
	}
	if status, _ := a.call(http.MethodGet, "/leave/employees/"+hrID+"/balance", mgr, ""); status != 403 {
		t.Fatalf("manager reads HR balance: %d", status)
	}
	if status, _ := a.call(http.MethodGet, "/leave/employees/"+hrID+"/ledger", worker, ""); status != 403 {
		t.Fatalf("employee reads colleague ledger: %d", status)
	}
	if status, l := a.call(http.MethodGet, "/leave/employees/"+workerID+"/ledger", worker, ""); status != 200 || len(l["items"].([]any)) == 0 {
		t.Fatalf("own ledger: %d %v", status, l)
	}

	start, end := futureWeek(6)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	cases := []struct {
		name, body string
		status     int
	}{
		{"for a colleague", `{"employee_id":` + hrID + `,"leave_type":"annual","start_date":"` + start + `","end_date":"` + end + `"}`, 403},
		{"in the past", leaveBody(yesterday, yesterday), 422},
		{"reversed range", leaveBody(end, start), 422},
		{"unknown type", `{"leave_type":"vacation","start_date":"` + start + `","end_date":"` + end + `"}`, 422},
		{"multi-day half day", `{"leave_type":"annual","start_date":"` + start + `","end_date":"` + end + `","half_day":"morning"}`, 422},
		{"too much", leaveBody(start, time.Now().AddDate(0, 6, 0).Format("2006-01-02")), 409},
	}
	for _, c := range cases {
		if status, body := a.call(http.MethodPost, "/leave/requests", worker, c.body); status != c.status {
			t.Errorf("%s: %d %v, want %d", c.name, status, body, c.status)
		}
	}

	// HR may file for anyone and in the past (e.g. a late sick report).
	if status, body := a.call(http.MethodPost, "/leave/requests", a.token("ik"),
		`{"employee_id":`+workerID+`,"leave_type":"sick","start_date":"`+yesterday+`","end_date":"`+yesterday+`"}`); status != 201 {
		t.Fatalf("HR files a past sick report: %d %v", status, body)
	}

	// Lists are scoped like everything else.
	_, list := a.call(http.MethodGet, "/leave/requests", worker, "")
	for _, it := range list["items"].([]any) {
		if it.(map[string]any)["employee_id"].(float64) != float64(a.empID("D0001")) {
			t.Fatalf("employee sees someone else's request: %v", it)
		}
	}
	if status, h := a.call(http.MethodGet, "/leave/holidays?year=2026", worker, ""); status != 200 || len(h["items"].([]any)) != 17 {
		t.Fatalf("holidays 2026: %d %v", status, h)
	}
	if status, _ := a.call(http.MethodPost, "/leave/holidays", worker, `{"date":"2027-03-09","name":"x"}`); status != 403 {
		t.Fatalf("employee adds holiday: %d", status)
	}
}

// A delegate decides team leave only while the delegation lasts.
func TestDelegateApprovesLeave(t *testing.T) {
	a := newDemoAPI(t)
	worker, mgr := a.token("calisan"), a.token("yonetici")
	start, end := futureWeek(5)

	// The manager is away and names the developer as delegate.
	today := time.Now().Format("2006-01-02")
	if status, body := a.call(http.MethodPost, "/delegations", mgr,
		`{"delegate_employee_id":`+id(a.empID("D0001"))+`,"start_date":"`+today+`","end_date":"`+end+`"}`); status != 201 {
		t.Fatalf("delegation: %d %v", status, body)
	}

	// Another team member: HR adds one to the software unit.
	status, emp := a.call(http.MethodPost, "/employees", a.token("ik"),
		`{"employee_no":"E-7001","first_name":"Ada","last_name":"Er","org_unit_id":`+id(a.unitID("yazilim"))+`,"hire_date":"2015-01-05"}`)
	if status != 201 {
		t.Fatalf("create employee: %d %v", status, emp)
	}
	status, req := a.call(http.MethodPost, "/leave/requests", a.token("ik"),
		`{"employee_id":`+id(int64(emp["id"].(float64)))+`,"leave_type":"annual","start_date":"`+start+`","end_date":"`+end+`"}`)
	if status != 201 {
		t.Fatalf("request: %d %v", status, req)
	}
	if status, body := a.call(http.MethodPost, "/leave/requests/"+id(int64(req["id"].(float64)))+"/approve", worker, ""); status != 200 {
		t.Fatalf("delegate approval: %d %v", status, body)
	}
}

func TestLeaveRejectGetAndHolidays(t *testing.T) {
	a := newDemoAPI(t)
	worker, mgr, hr := a.token("calisan"), a.token("yonetici"), a.token("ik")
	start, end := futureWeek(8)

	_, req := a.call(http.MethodPost, "/leave/requests", worker, leaveBody(start, end))
	rid := id(int64(req["id"].(float64)))
	if status, body := a.call(http.MethodPost, "/leave/requests/"+rid+"/reject", mgr, `{"note":"proje teslimi"}`); status != 200 ||
		body["status"] != "rejected" || body["decision_note"] != "proje teslimi" {
		t.Fatalf("reject: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodPost, "/leave/requests/"+rid+"/reject", mgr, ""); status != 409 {
		t.Fatalf("second decision: %d, want 409", status)
	}
	if status, body := a.call(http.MethodGet, "/leave/requests/"+rid, worker, ""); status != 200 || body["status"] != "rejected" {
		t.Fatalf("get own: %d %v", status, body)
	}
	if status, _ := a.call(http.MethodGet, "/leave/requests/"+rid, a.token("bordro"), ""); status != 200 {
		t.Fatalf("payroll reads all leave: %d", status)
	}
	if status, _ := a.call(http.MethodGet, "/leave/requests/999999", hr, ""); status != 404 {
		t.Fatalf("unknown request: %d", status)
	}

	body := `{"date":"2027-03-09","name":"Ramazan Bayramı 1. gün","half_day":false}`
	if status, h := a.call(http.MethodPost, "/leave/holidays", hr, body); status != 201 || h["date"] != "2027-03-09" {
		t.Fatalf("add holiday: %d %v", status, h)
	}
	if status, _ := a.call(http.MethodPost, "/leave/holidays", hr, body); status != 409 {
		t.Fatalf("duplicate holiday: %d", status)
	}
	if status, _ := a.call(http.MethodPost, "/leave/holidays", hr, `{"date":"9 Mart","name":""}`); status != 422 {
		t.Fatalf("invalid holiday: %d", status)
	}
}
