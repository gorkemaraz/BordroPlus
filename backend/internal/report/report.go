// Package report serves aggregate reports (reports.read): payroll summary
// by organisation unit (also as CSV), employer cost by month, leave usage
// and overtime. Reports aggregate by unit; individual salaries do not
// appear, so roles without salary.read (auditor, accounting) may read them.
package report

import (
	"encoding/csv"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/auth"
	"bordroplus/internal/httpx"
	"bordroplus/internal/leave"
	"bordroplus/internal/money"
)

type Handler struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewHandler(pool *pgxpool.Pool) *Handler { return &Handler{pool: pool, now: time.Now} }

func (h *Handler) Routes(r chi.Router) {
	g := r.With(auth.Require(auth.PermReportsRead))
	g.Get("/reports/payroll-summary", httpx.Handle(h.payrollSummary))
	g.Get("/reports/employer-cost", httpx.Handle(h.employerCost))
	g.Get("/reports/leave-usage", httpx.Handle(h.leaveUsage))
	g.Get("/reports/overtime", httpx.Handle(h.overtime))
}

// level is the org tree depth units are rolled up to (1 = whole company,
// 2 = departments under the root, …).
func level(r *http.Request) (int, error) {
	v := r.URL.Query().Get("level")
	if v == "" {
		return 2, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 10 {
		return 0, httpx.Validation(map[string]string{"level": "1-10"})
	}
	return n, nil
}

func month(r *http.Request, name string) (time.Time, error) {
	t, err := time.Parse("2006-01", r.URL.Query().Get(name))
	if err != nil {
		return time.Time{}, httpx.Validation(map[string]string{name: "YYYY-AA biçiminde zorunlu"})
	}
	return t, nil
}

// PayrollRow is one unit of the payroll summary.
type PayrollRow struct {
	UnitPath     string      `json:"unit_path"`
	UnitName     string      `json:"unit_name"`
	Headcount    int         `json:"headcount"`
	Gross        money.Kurus `json:"gross"`
	SGKEmployee  money.Kurus `json:"sgk_employee"`
	IncomeTax    money.Kurus `json:"income_tax"`
	StampTax     money.Kurus `json:"stamp_tax"`
	Net          money.Kurus `json:"net"`
	SGKEmployer  money.Kurus `json:"sgk_employer"`
	EmployerCost money.Kurus `json:"employer_cost"`
}

func kurusOf(field string) string {
	// Results store amounts as decimal strings ("123.45").
	return `sum(((r.result ->> '` + field + `')::numeric * 100)::bigint)`
}

// payrollSummary: ?period=YYYY-MM&level=2&format=csv.
func (h *Handler) payrollSummary(w http.ResponseWriter, r *http.Request) error {
	period, err := month(r, "period")
	if err != nil {
		return err
	}
	lv, err := level(r)
	if err != nil {
		return err
	}
	rows, err := h.pool.Query(r.Context(), `
		WITH x AS (
			SELECT subpath(u.path, 0, least(nlevel(u.path), $2)) AS unit, r.*
			  FROM payroll_results r
			  JOIN employees e ON e.id = r.employee_id
			  JOIN org_units u ON u.id = e.org_unit_id
			 WHERE r.period = $1)
		SELECT r.unit::text, COALESCE(n.name, r.unit::text), count(*), sum(r.gross),
		       `+kurusOf("sgk_employee")+`, sum(r.income_tax), `+kurusOf("stamp_tax")+`, sum(r.net),
		       `+kurusOf("sgk_employer")+`, sum(r.employer_cost)
		  FROM x r LEFT JOIN org_units n ON n.path = r.unit
		 GROUP BY r.unit, n.name ORDER BY r.unit`, period, lv)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (PayrollRow, error) {
		var p PayrollRow
		err := row.Scan(&p.UnitPath, &p.UnitName, &p.Headcount, &p.Gross, &p.SGKEmployee, &p.IncomeTax, &p.StampTax,
			&p.Net, &p.SGKEmployer, &p.EmployerCost)
		return p, err
	})
	if err != nil {
		return err
	}
	total := PayrollRow{UnitPath: "", UnitName: "Toplam"}
	for _, p := range items {
		total.Headcount += p.Headcount
		total.Gross += p.Gross
		total.SGKEmployee += p.SGKEmployee
		total.IncomeTax += p.IncomeTax
		total.StampTax += p.StampTax
		total.Net += p.Net
		total.SGKEmployer += p.SGKEmployer
		total.EmployerCost += p.EmployerCost
	}
	var status string
	if err := h.pool.QueryRow(r.Context(), `SELECT status FROM payroll_periods WHERE period = $1`, period).Scan(&status); err != nil {
		status = "not_run"
	}

	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="bordro-ozet-`+period.Format("2006-01")+`.csv"`)
		_, _ = w.Write([]byte("\xEF\xBB\xBF")) // BOM so spreadsheet tools read UTF-8
		cw := csv.NewWriter(w)
		cw.Comma = ';' // Turkish locale spreadsheets
		row := func(cells ...string) {
			for i := range cells {
				cells[i] = csvSafe(cells[i])
			}
			_ = cw.Write(cells)
		}
		row("Birim", "Yol", "Kişi", "Brüt", "SGK işçi", "Gelir vergisi", "Damga vergisi", "Net", "SGK işveren", "İşveren maliyeti")
		for _, p := range append(items, total) {
			row(p.UnitName, p.UnitPath, strconv.Itoa(p.Headcount), p.Gross.String(), p.SGKEmployee.String(),
				p.IncomeTax.String(), p.StampTax.String(), p.Net.String(), p.SGKEmployer.String(), p.EmployerCost.String())
		}
		cw.Flush()
		return cw.Error()
	}
	if items == nil {
		items = []PayrollRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"period": period.Format("2006-01"), "period_status": status, "level": lv, "items": items, "total": total,
	})
	return nil
}

type costRow struct {
	Period       string      `json:"period"`
	Status       string      `json:"status"`
	Headcount    int         `json:"headcount"`
	Gross        money.Kurus `json:"gross"`
	Net          money.Kurus `json:"net"`
	EmployerCost money.Kurus `json:"employer_cost"`
}

// employerCost: monthly payroll cost of a year, ?year=2026.
func (h *Handler) employerCost(w http.ResponseWriter, r *http.Request) error {
	year := h.now().Year()
	if v := r.URL.Query().Get("year"); v != "" {
		y, err := strconv.Atoi(v)
		if err != nil || y < 2000 || y > 2100 {
			return httpx.Validation(map[string]string{"year": "2000-2100"})
		}
		year = y
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT to_char(r.period, 'YYYY-MM'), COALESCE(p.status, 'not_run'), count(*), sum(r.gross), sum(r.net), sum(r.employer_cost)
		  FROM payroll_results r LEFT JOIN payroll_periods p ON p.period = r.period
		 WHERE r.period >= make_date($1, 1, 1) AND r.period < make_date($1 + 1, 1, 1)
		 GROUP BY r.period, p.status ORDER BY r.period`, year)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[costRow])
	if err != nil {
		return err
	}
	var total costRow
	total.Period = strconv.Itoa(year)
	for _, c := range items {
		total.Gross += c.Gross
		total.Net += c.Net
		total.EmployerCost += c.EmployerCost
	}
	if items == nil {
		items = []costRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"year": year, "items": items, "total": total})
	return nil
}

type leaveRow struct {
	UnitPath  string  `json:"unit_path"`
	UnitName  string  `json:"unit_name"`
	Headcount int     `json:"headcount"`
	Accrued   float64 `json:"accrued_days"`
	Used      float64 `json:"used_days"`
	Refunded  float64 `json:"refunded_days"`
	Balance   float64 `json:"balance_days"`
}

// leaveUsage: accrued, used and remaining annual leave per unit for active
// employees. Accruals are computed by the engine rather than read from the
// ledger, where they are booked lazily, so the report is complete without
// writing anything.
func (h *Handler) leaveUsage(w http.ResponseWriter, r *http.Request) error {
	lv, err := level(r)
	if err != nil {
		return err
	}
	y, m, d := h.now().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	rows, err := h.pool.Query(r.Context(), `
		SELECT subpath(u.path, 0, least(nlevel(u.path), $1))::text, e.hire_date, e.birth_date, e.termination_date,
		       COALESCE(-sum(l.half_days) FILTER (WHERE l.kind = 'usage'), 0),
		       COALESCE(sum(l.half_days) FILTER (WHERE l.kind IN ('sick_refund', 'reversal')), 0),
		       COALESCE(sum(l.half_days) FILTER (WHERE l.kind = 'adjustment'), 0)
		  FROM employees e
		  JOIN org_units u ON u.id = e.org_unit_id
		  LEFT JOIN leave_ledger l ON l.employee_id = e.id
		 WHERE e.termination_date IS NULL OR e.termination_date >= $2
		 GROUP BY 1, e.id`, lv, today)
	if err != nil {
		return err
	}
	byUnit := map[string]*leaveRow{}
	for rows.Next() {
		var unit string
		var hire time.Time
		var birth, term *time.Time
		var used, refunded, adjusted int
		if err := rows.Scan(&unit, &hire, &birth, &term, &used, &refunded, &adjusted); err != nil {
			rows.Close()
			return err
		}
		accrued := 0
		for _, a := range leave.Accruals(hire, birth, term, today) {
			accrued += a.HalfDays
		}
		u := byUnit[unit]
		if u == nil {
			u = &leaveRow{UnitPath: unit}
			byUnit[unit] = u
		}
		u.Headcount++
		u.Accrued += leave.Days(accrued)
		u.Used += leave.Days(used)
		u.Refunded += leave.Days(refunded)
		u.Balance += leave.Days(accrued - used + refunded + adjusted)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	names, err := unitNames(r, h.pool)
	if err != nil {
		return err
	}
	items := []leaveRow{}
	for _, u := range byUnit {
		u.UnitName = names[u.UnitPath]
		items = append(items, *u)
	}
	slices.SortFunc(items, func(a, b leaveRow) int { return strings.Compare(a.UnitPath, b.UnitPath) })
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"as_of": today.Format("2006-01-02"), "level": lv, "items": items})
	return nil
}

type overtimeRow struct {
	UnitPath        string `json:"unit_path"`
	UnitName        string `json:"unit_name"`
	ApprovedSheets  int    `json:"approved_timesheets"`
	OvertimeMinutes int    `json:"overtime_minutes"`
	RestDayMinutes  int    `json:"rest_day_minutes"`
	HolidayMinutes  int    `json:"holiday_minutes"`
}

// overtime: approved timesheet minutes per unit for ?month=YYYY-MM, and the
// employees whose overtime this year exceeds the 270-hour legal cap.
func (h *Handler) overtime(w http.ResponseWriter, r *http.Request) error {
	m, err := month(r, "month")
	if err != nil {
		return err
	}
	lv, err := level(r)
	if err != nil {
		return err
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT subpath(u.path, 0, least(nlevel(u.path), $2))::text, count(*),
		       COALESCE(sum((t.totals -> 'totals' ->> 'overtime_minutes')::int), 0),
		       COALESCE(sum((t.totals -> 'totals' ->> 'rest_day_minutes')::int), 0),
		       COALESCE(sum((t.totals -> 'totals' ->> 'holiday_minutes')::int), 0)
		  FROM timesheet_periods t
		  JOIN employees e ON e.id = t.employee_id
		  JOIN org_units u ON u.id = e.org_unit_id
		 WHERE t.month = $1 GROUP BY 1 ORDER BY 1`, m, lv)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (overtimeRow, error) {
		var o overtimeRow
		err := row.Scan(&o.UnitPath, &o.ApprovedSheets, &o.OvertimeMinutes, &o.RestDayMinutes, &o.HolidayMinutes)
		return o, err
	})
	if err != nil {
		return err
	}
	names, err := unitNames(r, h.pool)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].UnitName = names[items[i].UnitPath]
	}
	type capRow struct {
		EmployeeID      int64  `json:"employee_id"`
		EmployeeNo      string `json:"employee_no"`
		OvertimeMinutes int    `json:"year_overtime_minutes"`
	}
	rows, err = h.pool.Query(r.Context(), `
		SELECT e.id, e.employee_no, sum((t.totals -> 'totals' ->> 'overtime_minutes')::int)
		  FROM timesheet_periods t JOIN employees e ON e.id = t.employee_id
		 WHERE t.month >= date_trunc('year', $1::date) AND t.month <= $1
		 GROUP BY e.id, e.employee_no HAVING sum((t.totals -> 'totals' ->> 'overtime_minutes')::int) > 270 * 60
		 ORDER BY 3 DESC`, m)
	if err != nil {
		return err
	}
	over, err := pgx.CollectRows(rows, pgx.RowToStructByPos[capRow])
	if err != nil {
		return err
	}
	if items == nil {
		items = []overtimeRow{}
	}
	if over == nil {
		over = []capRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"month": m.Format("2006-01"), "level": lv, "items": items, "over_annual_cap": over})
	return nil
}

// csvSafe prevents CSV formula injection. A spreadsheet treats a cell that
// begins with =, +, - or @ (or a leading tab/carriage return) as a formula,
// so a unit name like "=HYPERLINK(...)" entered through the UI would run when
// the exported report is opened. Such a cell is prefixed with a single quote,
// which spreadsheets render as plain text. A genuine number (an optional sign
// then a digit, like the amounts and counts) is left untouched so it stays
// numeric.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '@', '+', '\t', '\r':
		return "'" + s
	case '-':
		// A leading minus followed by a digit is a negative amount
		// (Money.String never emits a leading +), not a formula like "-1+1".
		if len(s) > 1 && s[1] >= '0' && s[1] <= '9' {
			return s
		}
		return "'" + s
	}
	return s
}

func unitNames(r *http.Request, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(r.Context(), `SELECT path::text, name FROM org_units`)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for rows.Next() {
		var p, n string
		if err := rows.Scan(&p, &n); err != nil {
			rows.Close()
			return nil, err
		}
		out[p] = n
	}
	rows.Close()
	return out, rows.Err()
}
