package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"bordroplus/internal/leave"
	"bordroplus/internal/money"
	"bordroplus/internal/timesheet"
)

const dateFmt = "2006-01-02"

func today() time.Time { return timesheet.CivilDate(time.Now()) }

func (e *env) checkHealth() error {
	r, err := newClient(e.base).must(200, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	checks, _ := r.body["checks"].(map[string]any)
	return expect(r.str("status") == "ok" && checks["database"] == "ok", "beklenmeyen sağlık yanıtı: %s", r.raw)
}

func (e *env) checkAuth() error {
	anon := newClient(e.base)
	r, err := anon.must(401, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "denetci@bordroplus.test", "password": "yanlis-parola"})
	if err != nil {
		return err
	}
	if r.code() != "invalid_credentials" {
		return fmt.Errorf("yanlış parola kodu %q", r.code())
	}
	if _, err := anon.must(401, http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "yok@bordroplus.test", "password": "x"}); err != nil {
		return err
	}
	c := newClient(e.base)
	if err := c.login("calisan@bordroplus.test", e.password); err != nil {
		return err
	}
	old := c.token
	r, err = c.must(200, http.MethodPost, "/api/v1/auth/refresh", nil)
	if err != nil {
		return err
	}
	if r.str("access_token") == "" || r.str("access_token") == old {
		return fmt.Errorf("refresh yeni access token vermedi")
	}
	c.token = r.str("access_token")
	me, err := c.must(200, http.MethodGet, "/api/v1/auth/me", nil)
	if err != nil {
		return err
	}
	if me.str("role") != "employee" {
		return fmt.Errorf("me.role = %q", me.str("role"))
	}
	if _, err := c.must(204, http.MethodPost, "/api/v1/auth/logout", nil); err != nil {
		return err
	}
	_, err = c.must(401, http.MethodPost, "/api/v1/auth/refresh", nil)
	return err
}

func (e *env) checkAuthz() error {
	anon := newClient(e.base)
	r, err := anon.must(401, http.MethodGet, path("/auth/me"), nil)
	if err != nil {
		return err
	}
	if r.code() != "unauthorized" {
		return fmt.Errorf("hata biçimi: %s", r.raw)
	}
	if _, err := e.u("calisan").must(403, http.MethodGet, path("/audit-log"), nil); err != nil {
		return err
	}
	if _, err := e.u("bordro").must(403, http.MethodPost, path("/employees"), map[string]any{"employee_no": "X-1"}); err != nil {
		return err
	}
	if _, err := e.u("yonetici").must(403, http.MethodPost, path("/payroll/periods/2025-01/run"), nil); err != nil {
		return err
	}
	fake := newClient(e.base)
	fake.token = e.u("calisan").token + "x"
	_, err = fake.must(401, http.MethodGet, path("/auth/me"), nil)
	return err
}

func (e *env) checkEmployees() error {
	hr := e.u("ik")
	r, err := hr.must(200, http.MethodGet, path("/org/units"), nil)
	if err != nil {
		return err
	}
	e.unitID = map[string]int64{}
	for _, u := range r.items() {
		e.unitID[u["code"].(string)] = int64(u["id"].(float64))
	}
	if len(e.unitID) < 20 || e.unitID["yazilim"] == 0 {
		return fmt.Errorf("organizasyon ağacında %d birim var", len(e.unitID))
	}
	if e.verifyEmp, err = e.createEmployee("V"+e.runID, "Çalışan"+e.runID, "2024-01-02", "65000.00", "2025-01-01"); err != nil {
		return err
	}
	card, err := hr.must(200, http.MethodGet, path("/employees/%d", e.verifyEmp), nil)
	if err != nil {
		return err
	}
	sal, _ := card.body["salary"].(map[string]any)
	if card.str("org_unit_path") != "genel_mudurluk.bilgi_teknolojileri.yazilim" || sal == nil {
		return fmt.Errorf("kart: %s", card.raw)
	}
	p1, err := hr.must(200, http.MethodGet, path("/employees?limit=50"), nil)
	if err != nil {
		return err
	}
	if len(p1.items()) != 50 || p1.str("next_cursor") == "" {
		return fmt.Errorf("ilk sayfa %d kayıt, imleç %q", len(p1.items()), p1.str("next_cursor"))
	}
	p2, err := hr.must(200, http.MethodGet, path("/employees?limit=50&cursor=%s", p1.str("next_cursor")), nil)
	if err != nil {
		return err
	}
	if len(p2.items()) == 0 || p2.items()[0]["id"].(float64) <= p1.items()[49]["id"].(float64) {
		return fmt.Errorf("keyset sayfalama ilerlemedi")
	}
	s, err := hr.must(200, http.MethodGet, path("/employees?q=V%s", e.runID), nil)
	if err != nil {
		return err
	}
	return expect(len(s.items()) == 1, "arama %d sonuç döndürdü", len(s.items()))
}

func (e *env) createEmployee(no, last, hire, salary, from string) (int64, error) {
	r, err := e.u("ik").must(201, http.MethodPost, path("/employees"), map[string]any{
		"employee_no": no, "first_name": "Doğrulama", "last_name": last, "title": "Doğrulama Çalışanı",
		"org_unit_id": e.unitID["yazilim"], "hire_date": hire,
		"salary": map[string]string{"amount": salary, "valid_from": from},
	})
	if err != nil {
		return 0, err
	}
	return int64(r.num("id")), nil
}

func (e *env) checkIdempotency() error {
	if e.unitID == nil {
		return errPrereq
	}
	body := map[string]any{"employee_no": "I" + e.runID, "first_name": "Tekrar", "last_name": "Deneme",
		"org_unit_id": e.unitID["yazilim"], "hire_date": "2025-06-02"}
	key := "verify-" + e.runID
	a, err := e.u("ik").must(201, http.MethodPost, path("/employees"), body, "Idempotency-Key", key)
	if err != nil {
		return err
	}
	b, err := e.u("ik").must(201, http.MethodPost, path("/employees"), body, "Idempotency-Key", key)
	if err != nil {
		return err
	}
	if a.num("id") != b.num("id") || b.header.Get("Idempotent-Replayed") != "true" {
		return fmt.Errorf("tekrar istek yeni kayıt oluşturdu (%v / %v)", a.num("id"), b.num("id"))
	}
	body["first_name"] = "Başka"
	r, err := e.u("ik").must(422, http.MethodPost, path("/employees"), body, "Idempotency-Key", key)
	if err != nil {
		return err
	}
	return expect(r.code() == "idempotency_key_reused", "kod %q", r.code())
}

func (e *env) checkScope() error {
	if e.verifyEmp == 0 {
		return errPrereq
	}
	mgr, worker := e.u("yonetici"), e.u("calisan")
	team, err := mgr.must(200, http.MethodGet, path("/employees?limit=200"), nil)
	if err != nil {
		return err
	}
	for _, it := range team.items() {
		if !strings.HasPrefix(it["org_unit_path"].(string), "genel_mudurluk.bilgi_teknolojileri") {
			return fmt.Errorf("yönetici alt ağacı dışındaki %v kaydını görüyor", it["employee_no"])
		}
	}
	if len(team.items()) < 3 {
		return fmt.Errorf("yönetici yalnızca %d çalışan görüyor", len(team.items()))
	}
	if _, err := mgr.must(403, http.MethodGet, path("/employees/%d", e.u("ik").employeeID()), nil); err != nil {
		return err
	}
	masked, err := e.u("denetci").must(200, http.MethodGet, path("/employees/%d", e.verifyEmp), nil)
	if err != nil {
		return err
	}
	if masked.body["salary_masked"] != true || masked.body["salary"] != nil {
		return fmt.Errorf("denetçi maaşı açık görüyor")
	}
	own, err := worker.must(200, http.MethodGet, path("/employees/%d", worker.employeeID()), nil)
	if err != nil {
		return err
	}
	if own.body["salary"] == nil {
		return fmt.Errorf("çalışan kendi maaşını göremiyor")
	}

	// Delegation: clear leftovers of an interrupted run, then delegate for today.
	act, err := mgr.must(200, http.MethodGet, path("/delegations?active=true"), nil)
	if err != nil {
		return err
	}
	for _, d := range act.items() {
		if int64(d["manager_employee_id"].(float64)) == mgr.employeeID() {
			_, _ = mgr.do(http.MethodPost, path("/delegations/%v/revoke", d["id"]), nil)
		}
	}
	day := today().Format(dateFmt)
	d, err := mgr.must(201, http.MethodPost, path("/delegations"), map[string]any{
		"delegate_employee_id": worker.employeeID(), "start_date": day, "end_date": day, "reason": "doğrulama"})
	if err != nil {
		return err
	}
	me, _ := worker.must(200, http.MethodGet, path("/auth/me"), nil)
	perms, _ := me.body["permissions"].([]any)
	if !slices.Contains(perms, any("leave.approve.team")) {
		return fmt.Errorf("vekil ekip iznini almadı")
	}
	seen, _ := worker.must(200, http.MethodGet, path("/employees?limit=200"), nil)
	if len(seen.items()) < 3 {
		return fmt.Errorf("vekil ekibi göremiyor")
	}
	if _, err := mgr.must(200, http.MethodPost, path("/delegations/%v/revoke", d.num("id")), nil); err != nil {
		return err
	}
	after, _ := worker.must(200, http.MethodGet, path("/employees?limit=200"), nil)
	return expect(len(after.items()) == 1, "vekâlet iptalinden sonra %d kayıt görünüyor", len(after.items()))
}

// freeWeeks returns Mondays at least three weeks ahead whose Monday-Friday
// contain no public holiday.
func (e *env) freeWeeks(n int) ([]time.Time, error) {
	hol := map[string]bool{}
	for _, y := range []int{today().Year(), today().Year() + 1} {
		r, err := e.u("ik").must(200, http.MethodGet, path("/leave/holidays?year=%d", y), nil)
		if err != nil {
			return nil, err
		}
		for _, h := range r.items() {
			hol[h["date"].(string)] = true
		}
	}
	var out []time.Time
	d := today().AddDate(0, 0, 21)
	for d.Weekday() != time.Monday {
		d = d.AddDate(0, 0, 1)
	}
	for ; len(out) < n; d = d.AddDate(0, 0, 7) {
		free := true
		for i := range 5 {
			free = free && !hol[d.AddDate(0, 0, i).Format(dateFmt)]
		}
		if free {
			out = append(out, d)
		}
	}
	return out, nil
}

func (e *env) leaveReq(c *client, want int, emp int64, typ string, start, end time.Time, half string) (response, error) {
	body := map[string]any{"leave_type": typ, "start_date": start.Format(dateFmt), "end_date": end.Format(dateFmt), "half_day": half}
	if emp != 0 {
		body["employee_id"] = emp
	}
	return c.must(want, http.MethodPost, path("/leave/requests"), body)
}

func (e *env) checkLeaveEngine() error {
	if e.verifyEmp == 0 {
		return errPrereq
	}
	hr, mgr := e.u("ik"), e.u("yonetici")
	bal, err := hr.must(200, http.MethodGet, path("/leave/employees/%d/balance", e.verifyEmp), nil)
	if err != nil {
		return err
	}
	hire, _ := time.Parse(dateFmt, "2024-01-02")
	want := 0
	acc := leave.Accruals(hire, nil, nil, today())
	for _, a := range acc {
		want += a.HalfDays
	}
	if bal.num("total_accrued") != leave.Days(want) || int(bal.num("service_years")) != len(acc) {
		return fmt.Errorf("hakediş %v gün / %v yıl, beklenen %v / %d", bal.num("total_accrued"), bal.num("service_years"), leave.Days(want), len(acc))
	}
	accrued := bal.num("balance")

	weeks, err := e.freeWeeks(3)
	if err != nil {
		return err
	}
	a, err := e.leaveReq(hr, 201, e.verifyEmp, "annual", weeks[0], weeks[0].AddDate(0, 0, 4), "")
	if err != nil {
		return err
	}
	b, err := e.leaveReq(hr, 201, e.verifyEmp, "annual", weeks[1], weeks[1].AddDate(0, 0, 4), "")
	if err != nil {
		return err
	}
	// Six more weeks no longer fit what is available (balance minus pending).
	c, err := e.leaveReq(hr, 409, e.verifyEmp, "annual", weeks[2], weeks[2].AddDate(0, 0, 40), "")
	if err != nil {
		return err
	}
	if c.code() != "insufficient_balance" {
		return fmt.Errorf("kod %q", c.code())
	}
	// Two approvers approve the two requests at the same moment.
	errs := parallel(
		func() error {
			_, err := mgr.must(200, http.MethodPost, path("/leave/requests/%v/approve", a.num("id")), nil)
			return err
		},
		func() error {
			_, err := hr.must(200, http.MethodPost, path("/leave/requests/%v/approve", b.num("id")), nil)
			return err
		},
	)
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	bal, _ = hr.must(200, http.MethodGet, path("/leave/employees/%d/balance", e.verifyEmp), nil)
	if bal.num("balance") != accrued-10 || bal.num("balance") < 0 {
		return fmt.Errorf("eşzamanlı onay sonrası bakiye %v, beklenen %v", bal.num("balance"), accrued-10)
	}
	// Sick report on Wednesday-Thursday of the first leave: two days back.
	s, err := e.leaveReq(hr, 201, e.verifyEmp, "sick", weeks[0].AddDate(0, 0, 2), weeks[0].AddDate(0, 0, 3), "")
	if err != nil {
		return err
	}
	if _, err := mgr.must(200, http.MethodPost, path("/leave/requests/%v/approve", s.num("id")), nil); err != nil {
		return err
	}
	bal, _ = hr.must(200, http.MethodGet, path("/leave/employees/%d/balance", e.verifyEmp), nil)
	if bal.num("balance") != accrued-8 || bal.num("total_refunded") != 2 {
		return fmt.Errorf("rapor iadesi sonrası bakiye %v (iade %v), beklenen %v", bal.num("balance"), bal.num("total_refunded"), accrued-8)
	}
	half, err := e.leaveReq(hr, 201, e.verifyEmp, "annual", weeks[2].AddDate(0, 0, 1), weeks[2].AddDate(0, 0, 1), "morning")
	if err != nil {
		return err
	}
	if half.num("days") != 0.5 {
		return fmt.Errorf("yarım gün izin %v gün", half.num("days"))
	}
	if _, err := hr.must(200, http.MethodPost, path("/leave/requests/%v/cancel", half.num("id")), nil); err != nil {
		return err
	}
	led, err := hr.must(200, http.MethodGet, path("/leave/employees/%d/ledger", e.verifyEmp), nil)
	if err != nil {
		return err
	}
	rows := led.items()
	return expect(len(rows) > 0 && rows[len(rows)-1]["running_balance"].(float64) == bal.num("balance"),
		"defter toplamı bakiyeyle uyuşmuyor")
}

func (e *env) checkLeaveFlow() error {
	worker, mgr := e.u("calisan"), e.u("yonetici")
	weeks, err := e.freeWeeks(4)
	if err != nil {
		return err
	}
	r, err := e.leaveReq(worker, 201, 0, "annual", weeks[3], weeks[3], "")
	if err != nil {
		return err
	}
	if r.str("status") != "pending" {
		return fmt.Errorf("durum %q", r.str("status"))
	}
	q, err := mgr.must(200, http.MethodGet, path("/leave/requests?for_approval=true&limit=200"), nil)
	if err != nil {
		return err
	}
	found := slices.ContainsFunc(q.items(), func(it map[string]any) bool { return it["id"] == r.body["id"] })
	if !found {
		return fmt.Errorf("talep yöneticinin onay kuyruğunda yok")
	}
	if _, err := worker.must(403, http.MethodPost, path("/leave/requests/%v/approve", r.num("id")), nil); err != nil {
		return err
	}
	if _, err := mgr.must(200, http.MethodPost, path("/leave/requests/%v/reject", r.num("id")), map[string]string{"note": "doğrulama"}); err != nil {
		return err
	}
	got, err := worker.must(200, http.MethodGet, path("/leave/requests/%v", r.num("id")), nil)
	if err != nil {
		return err
	}
	return expect(got.str("status") == "rejected", "durum %q", got.str("status"))
}

// Week of Monday 3 November 2025 (no holidays): a clean day, a double
// punch, a missing out, a night shift, Saturday and Sunday work.
var tsWeek = []struct{ at, dir string }{
	{"2025-11-03T08:00:00+03:00", "in"}, {"2025-11-03T17:00:00+03:00", "out"},
	{"2025-11-04T08:00:00+03:00", "in"}, {"2025-11-04T08:02:00+03:00", "in"}, {"2025-11-04T17:30:00+03:00", "out"},
	{"2025-11-05T08:00:00+03:00", "in"},
	{"2025-11-06T22:00:00+03:00", "in"}, {"2025-11-07T06:00:00+03:00", "out"},
	{"2025-11-08T09:00:00+03:00", "in"}, {"2025-11-08T14:00:00+03:00", "out"},
	{"2025-11-09T10:00:00+03:00", "in"}, {"2025-11-09T14:00:00+03:00", "out"},
}

func (e *env) checkTimesheet() error {
	if e.verifyEmp == 0 {
		return errPrereq
	}
	hr, mgr := e.u("ik"), e.u("yonetici")
	var ps []map[string]any
	for _, p := range tsWeek {
		ps = append(ps, map[string]any{"employee_id": e.verifyEmp, "punched_at": p.at, "direction": p.dir, "device_id": "VERIFY"})
	}
	imp, err := hr.must(201, http.MethodPost, path("/timesheet/punches"), map[string]any{"punches": ps})
	if err != nil {
		return err
	}
	if imp.num("inserted") != float64(len(tsWeek)) {
		return fmt.Errorf("%v basım eklendi", imp.num("inserted"))
	}
	days, err := mgr.must(200, http.MethodGet, path("/timesheet/employees/%d/days?from=2025-11-03&to=2025-11-09", e.verifyEmp), nil)
	if err != nil {
		return err
	}
	anomalies, _ := days.body["anomalies"].([]any)
	corrections, _ := days.body["corrections"].([]any)
	if len(anomalies) != 2 || len(corrections) != 2 {
		return fmt.Errorf("%d anomali, %d öneri (beklenen 2/2)", len(anomalies), len(corrections))
	}
	// The night shift is split across Thursday and Friday.
	night := false
	for _, d := range days.body["days"].([]any) {
		for _, s := range d.(map[string]any)["sessions"].([]any) {
			if segs := s.(map[string]any)["segments"].([]any); len(segs) == 2 {
				night = true
			}
		}
	}
	if !night {
		return fmt.Errorf("gece vardiyası iki güne bölünmedi")
	}
	// Month approval is refused while proposals are open.
	r, err := mgr.must(409, http.MethodPost, path("/timesheet/employees/%d/months/2025-11/approve", e.verifyEmp), nil)
	if err != nil {
		return err
	}
	if r.code() != "unresolved_anomalies" {
		return fmt.Errorf("kod %q", r.code())
	}
	for _, c := range corrections {
		cm := c.(map[string]any)
		var body any
		if cm["kind"] == "add_missing_out" {
			body = map[string]string{"at": "2025-11-05T17:00:00+03:00", "note": "doğrulama"}
		}
		if _, err := mgr.must(200, http.MethodPost, path("/timesheet/corrections/%v/approve", cm["id"]), body); err != nil {
			return err
		}
	}
	return nil
}

func (e *env) checkTimesheetMonth() error {
	if e.verifyEmp == 0 {
		return errPrereq
	}
	mgr := e.u("yonetici")
	m, err := mgr.must(200, http.MethodPost, path("/timesheet/employees/%d/months/2025-11/approve", e.verifyEmp), nil)
	if err != nil {
		return err
	}
	t, _ := m.body["totals"].(map[string]any)
	// Mon 480, Tue 510, Wed 480, night 120+300, Sat 270 ordinary; Sun 225 rest day.
	if t["normal_minutes"] != 2160.0 || t["rest_day_minutes"] != 225.0 || t["overtime_minutes"] != 0.0 || t["worked_days"] != 7.0 {
		return fmt.Errorf("ay toplamları %v", t)
	}
	r, err := e.u("ik").must(409, http.MethodPost, path("/timesheet/punches"), map[string]any{"punches": []map[string]any{
		{"employee_id": e.verifyEmp, "punched_at": "2025-11-05T18:00:00+03:00", "direction": "in"}}})
	if err != nil {
		return err
	}
	return expect(r.code() == "period_locked", "kod %q", r.code())
}

func kurus(v any) money.Kurus {
	s, _ := v.(string)
	k, err := money.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("tutar %q okunamadı", s))
	}
	return k
}

// pickPeriod: the earliest payroll month of 2025-2026 that is not closed.
func (e *env) pickPeriod() error {
	r, err := e.u("bordro").must(200, http.MethodGet, path("/payroll/periods"), nil)
	if err != nil {
		return err
	}
	closed := map[string]bool{}
	for _, p := range r.items() {
		if p["status"] == "closed" {
			closed[p["period"].(string)] = true
		}
	}
	for m := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC); m.Year() <= 2026; m = m.AddDate(0, 1, 0) {
		if !closed[m.Format("2006-01")] {
			e.period = m
			return nil
		}
	}
	return fmt.Errorf("2025-2026 dönemlerinin hepsi kapalı; make reset-demo çalıştırın")
}

func (e *env) checkPayrollEngine() error {
	if e.verifyEmp == 0 {
		return errPrereq
	}
	if err := e.pickPeriod(); err != nil {
		return err
	}
	p := e.period.Format("2006-01")
	e.note("bordro dönemi: %s", p)
	pr := e.u("bordro")

	// A minimum wage employee hired on the first day of the period, so no
	// earlier month (and no prior-period line) is involved.
	mwGross := map[int]string{2025: "26005.50", 2026: "33030.00"}[e.period.Year()]
	var err error
	if e.minWage, err = e.createEmployee("W"+e.runID, "Asgari"+e.runID, e.period.Format(dateFmt), mwGross, e.period.Format(dateFmt)); err != nil {
		return err
	}

	mw, err := pr.must(200, http.MethodPost, path("/payroll/employees/%d/calculate", e.minWage), map[string]string{"period": p})
	if err != nil {
		return err
	}
	published := map[int]string{2025: "22104.67", 2026: "28075.50"}[e.period.Year()]
	if mw.str("net") != published || mw.str("income_tax") != "0.00" || mw.str("stamp_tax") != "0.00" {
		return fmt.Errorf("asgari ücret neti %s (yayımlanan %s)", mw.str("net"), published)
	}

	r, err := pr.must(200, http.MethodPost, path("/payroll/employees/%d/calculate", e.verifyEmp), map[string]string{"period": p})
	if err != nil {
		return err
	}
	gross, net := kurus(r.body["gross"]), kurus(r.body["net"])
	sgk, unemp := kurus(r.body["sgk_employee"]), kurus(r.body["unemp_employee"])
	tax, stamp, ded := kurus(r.body["income_tax"]), kurus(r.body["stamp_tax"]), kurus(r.body["net_deductions"])
	if gross-sgk-unemp-tax-stamp-ded != net || kurus(r.body["tax_base"]) != gross-sgk-unemp {
		return fmt.Errorf("bordro satırları tutarsız: %s", r.raw)
	}
	if e.period.Month() == time.December {
		e.note("T-3: %s aralık; sonraki ay zinciri yıl sonunda kontrol edilmedi", p)
		return nil
	}
	next := e.period.AddDate(0, 1, 0).Format("2006-01")
	r2, err := pr.must(200, http.MethodPost, path("/payroll/employees/%d/calculate", e.verifyEmp), map[string]string{"period": next})
	if err != nil {
		return err
	}
	if kurus(r2.body["cum_tax_base"]) != kurus(r.body["cum_tax_base"])+kurus(r2.body["tax_base"]) {
		return fmt.Errorf("kümülatif matrah zinciri kopuk")
	}
	// Retroactive raise in the middle of the open month: both months change.
	raise := e.period.AddDate(0, 0, 14).Format(dateFmt)
	if _, err := e.u("ik").must(201, http.MethodPost, path("/employees/%d/salaries", e.verifyEmp), map[string]string{"amount": "70000.00", "valid_from": raise}); err != nil {
		return err
	}
	rc, err := pr.must(200, http.MethodPost, path("/payroll/employees/%d/recalculate", e.verifyEmp), map[string]string{"from": p})
	if err != nil {
		return err
	}
	changes, _ := rc.body["changes"].([]any)
	if len(changes) != 2 {
		return fmt.Errorf("%d ay yeniden hesaplandı, beklenen 2", len(changes))
	}
	for _, c := range changes {
		cm := c.(map[string]any)
		before, _ := cm["before"].(map[string]any)
		after, _ := cm["after"].(map[string]any)
		if cm["changed"] != true || before == nil || before["cum_tax_base"] == after["cum_tax_base"] {
			return fmt.Errorf("%v ayı güncellenmedi", cm["period"])
		}
	}
	return nil
}

func (e *env) checkPayrollWorkflow() error {
	if e.period.IsZero() {
		return errPrereq
	}
	p := e.period.Format("2006-01")
	pr := e.u("bordro")
	run, err := pr.must(200, http.MethodPost, path("/payroll/periods/%s/run", p), nil)
	if err != nil {
		return err
	}
	fails, _ := run.body["failures"].([]any)
	if len(fails) > 0 {
		return fmt.Errorf("%d çalışan hesaplanamadı, ilki: %v", len(fails), fails[0])
	}
	if run.num("employees") < 1000 || run.num("duration_ms") >= 10000 {
		return fmt.Errorf("%v çalışan %v ms", run.num("employees"), run.num("duration_ms"))
	}
	e.note("T-4: %s dönemi %.0f çalışan, %.0f ms, %.0f işçi", p, run.num("employees"), run.num("duration_ms"), run.num("workers"))
	again, err := pr.must(200, http.MethodPost, path("/payroll/periods/%s/run", p), nil)
	if err != nil {
		return err
	}
	if again.str("digest") != run.str("digest") || again.num("changed") != 0 {
		return fmt.Errorf("aynı kurallarla tekrar hesap farklı sonuç verdi")
	}
	_, err = pr.must(200, http.MethodPost, path("/payroll/periods/%s/submit", p), nil)
	return err
}

func (e *env) checkPayrollApproval() error {
	if e.period.IsZero() {
		return errPrereq
	}
	p := e.period.Format("2006-01")
	if _, err := e.u("bordro").must(403, http.MethodPost, path("/payroll/periods/%s/approve", p), nil); err != nil {
		return err
	}
	if _, err := e.u("muhasebe").must(200, http.MethodPost, path("/payroll/periods/%s/approve", p), nil); err != nil {
		return err
	}
	r, err := e.u("bordro").must(409, http.MethodPost, path("/payroll/employees/%d/calculate", e.verifyEmp), map[string]string{"period": p})
	if err != nil {
		return err
	}
	return expect(r.code() == "period_frozen", "kod %q", r.code())
}

func (e *env) checkPeriodClose() error {
	if e.period.IsZero() {
		return errPrereq
	}
	p := e.period.Format("2006-01")
	acc := e.u("muhasebe")
	if _, err := acc.must(200, http.MethodPost, path("/payroll/periods/%s/close", p), nil); err != nil {
		return err
	}
	r, err := acc.must(409, http.MethodPost, path("/payroll/periods/%s/close", p), nil)
	if err != nil {
		return err
	}
	if r.code() != "invalid_transition" {
		return fmt.Errorf("kod %q", r.code())
	}
	r, err = e.u("bordro").must(409, http.MethodPost, path("/payroll/periods/%s/run", p), nil)
	if err != nil {
		return err
	}
	return expect(r.code() == "period_frozen", "kod %q", r.code())
}

func (e *env) checkItemsAndRetro() error {
	if e.period.IsZero() {
		return errPrereq
	}
	next := e.period.AddDate(0, 1, 0)
	if next.Year() > 2026 {
		e.note("F-5: %s için kural seti yok; atlandı", next.Format("2006-01"))
		return nil
	}
	// After the close, a raise effective inside the closed month arrives.
	raise := e.period.AddDate(0, 0, 20).Format(dateFmt)
	if _, err := e.u("ik").must(201, http.MethodPost, path("/employees/%d/salaries", e.verifyEmp), map[string]string{"amount": "72000.00", "valid_from": raise}); err != nil {
		return err
	}
	pr := e.u("bordro")
	if _, err := pr.must(201, http.MethodPost, path("/payroll/items"), map[string]any{
		"employee_id": e.verifyEmp, "kind": "loan", "label": "Doğrulama borcu", "amount": "3000.00", "instalments": 3,
		"start_period": next.Format("2006-01")}); err != nil {
		return err
	}
	run, err := pr.must(200, http.MethodPost, path("/payroll/periods/%s/run", next.Format("2006-01")), nil)
	if err != nil {
		return err
	}
	if fails, _ := run.body["failures"].([]any); len(fails) > 0 {
		return fmt.Errorf("%d çalışan hesaplanamadı", len(fails))
	}
	r, err := pr.must(200, http.MethodGet, path("/payroll/employees/%d/results/%s", e.verifyEmp, next.Format("2006-01")), nil)
	if err != nil {
		return err
	}
	res, _ := r.body["result"].(map[string]any)
	var retro, loan bool
	for _, l := range res["lines"].([]any) {
		lm := l.(map[string]any)
		retro = retro || lm["code"] == "retro_"+e.period.Format("2006-01")
		loan = loan || strings.Contains(lm["label"].(string), "(1/3)")
	}
	return expect(retro && loan, "geçmiş dönem satırı %v, borç taksiti %v", retro, loan)
}

func (e *env) checkReports() error {
	if e.period.IsZero() {
		return errPrereq
	}
	p := e.period.Format("2006-01")
	aud := e.u("denetci")
	s, err := aud.must(200, http.MethodGet, path("/reports/payroll-summary?period=%s", p), nil)
	if err != nil {
		return err
	}
	total, _ := s.body["total"].(map[string]any)
	if s.str("period_status") != "closed" || total["headcount"].(float64) < 1000 {
		return fmt.Errorf("özet: %s", s.raw[:min(300, len(s.raw))])
	}
	csv, err := aud.must(200, http.MethodGet, path("/reports/payroll-summary?period=%s&format=csv", p), nil)
	if err != nil {
		return err
	}
	if !strings.Contains(csv.raw, "Birim;Yol;Kişi;Brüt") {
		return fmt.Errorf("CSV başlığı yok")
	}
	html, err := e.u("bordro").must(200, http.MethodGet, path("/payroll/employees/%d/payslips/%s?format=html", e.verifyEmp, p), nil)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(html.header.Get("Content-Type"), "text/html") || !strings.Contains(html.raw, "Çalışan"+e.runID) {
		return fmt.Errorf("bordro çıktısı beklenen içerikte değil")
	}
	if _, err := e.u("calisan").must(403, http.MethodGet, path("/payroll/employees/%d/payslips/%s", e.verifyEmp, p), nil); err != nil {
		return err
	}
	cost, err := aud.must(200, http.MethodGet, path("/reports/employer-cost?year=%d", e.period.Year()), nil)
	if err != nil {
		return err
	}
	return expect(len(cost.items()) > 0, "maliyet raporu boş")
}

func (e *env) checkAudit() error {
	r, err := e.u("denetci").must(200, http.MethodGet, path("/audit-log?action=payroll.close&limit=5"), nil)
	if err != nil {
		return err
	}
	if len(r.items()) == 0 {
		return fmt.Errorf("dönem kapanışı denetim kaydında yok")
	}
	_, err = e.u("calisan").must(403, http.MethodGet, path("/audit-log"), nil)
	return err
}
