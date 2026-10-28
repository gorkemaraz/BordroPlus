package payroll

import (
	"context"
	"errors"
	"html/template"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrPayslipNotFinal: employees see their payslip only once the period is
// approved or closed.
var ErrPayslipNotFinal = errors.New("payslip not final yet")

// Payslip is the employee-facing document of a month.
type Payslip struct {
	Company      string     `json:"company"`
	Period       string     `json:"period"`
	PeriodStatus string     `json:"period_status"` // draft (not run as a period) | review | approved | closed
	Final        bool       `json:"final"`
	Employee     slipPerson `json:"employee"`
	PaidDays     int        `json:"paid_days"`
	RuleVersion  string     `json:"rule_version"`
	Lines        []Line     `json:"lines"`
	Gross        Kurus      `json:"gross"`
	Deductions   Kurus      `json:"total_deductions"`
	Net          Kurus      `json:"net"`
	EmployerCost Kurus      `json:"employer_cost"`
	Notes        []string   `json:"notes"`
	GeneratedAt  time.Time  `json:"generated_at"`
}

type slipPerson struct {
	ID         int64  `json:"id"`
	EmployeeNo string `json:"employee_no"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Unit       string `json:"unit"`
	HireDate   string `json:"hire_date"`
}

// CompanyName appears on payslips.
const CompanyName = "BordroPlus Demo A.Ş. (kurgusal)"

// GetPayslip assembles a stored month. finalOnly restricts it to approved
// or closed periods (the employee's own view).
func (s *Service) GetPayslip(ctx context.Context, employeeID int64, period time.Time, finalOnly bool) (Payslip, error) {
	period = MonthStart(period)
	status := "draft"
	err := s.pool.QueryRow(ctx, `SELECT status FROM payroll_periods WHERE period = $1`, period).Scan(&status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Payslip{}, err
	}
	final := status == StatusApproved || status == StatusClosed
	if finalOnly && !final {
		return Payslip{}, ErrPayslipNotFinal
	}
	r, in, err := s.StoredResult(ctx, employeeID, period)
	if err != nil {
		return Payslip{}, err
	}
	p := Payslip{
		Company: CompanyName, Period: period.Format("2006-01"), PeriodStatus: status, Final: final,
		PaidDays: in.Input.PaidDays, RuleVersion: r.RuleVersion, Lines: r.Lines,
		Gross: r.Gross, Net: r.Net, EmployerCost: r.EmployerCost, Notes: in.Context.Notes, GeneratedAt: time.Now().UTC(),
	}
	p.Deductions = r.Gross - r.Net
	if p.Notes == nil {
		p.Notes = []string{}
	}
	err = s.pool.QueryRow(ctx, `
		SELECT e.id, e.employee_no, e.first_name || ' ' || e.last_name, e.title, u.name, to_char(e.hire_date, 'YYYY-MM-DD')
		  FROM employees e JOIN org_units u ON u.id = e.org_unit_id WHERE e.id = $1`, employeeID).
		Scan(&p.Employee.ID, &p.Employee.EmployeeNo, &p.Employee.Name, &p.Employee.Title, &p.Employee.Unit, &p.Employee.HireDate)
	return p, err
}

// payslipHTML is a print-ready page (the browser's "save as PDF" produces
// the document). html/template escapes every value.
var payslipHTML = template.Must(template.New("payslip").Parse(`<!doctype html>
<html lang="tr"><head><meta charset="utf-8"><title>Bordro {{.Period}} - {{.Employee.Name}}</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;margin:24px;color:#111}
h1{font-size:20px;margin:0}.muted{color:#555}table{width:100%;border-collapse:collapse;margin-top:16px}
td,th{padding:6px 8px;border-bottom:1px solid #ddd;text-align:left}td.n{text-align:right;font-variant-numeric:tabular-nums}
tr.total td{font-weight:600;border-top:2px solid #111}.draft{color:#b00;font-weight:600}
@media print{.noprint{display:none}body{margin:0}}
</style></head><body>
<h1>{{.Company}} — Ücret Bordrosu</h1>
<p class="muted">Dönem {{.Period}} · Kural seti {{.RuleVersion}} · {{.PaidDays}} gün
{{if not .Final}}<span class="draft"> · KESİNLEŞMEMİŞ ({{.PeriodStatus}})</span>{{end}}</p>
<p><strong>{{.Employee.Name}}</strong> ({{.Employee.EmployeeNo}})<br>{{.Employee.Title}} · {{.Employee.Unit}} · İşe giriş {{.Employee.HireDate}}</p>
<table><thead><tr><th>Kalem</th><th>Açıklama</th><th class="n">Tutar (TL)</th></tr></thead><tbody>
{{range .Lines}}<tr{{if or (eq .Code "gross") (eq .Code "net") (eq .Code "employer_cost")}} class="total"{{end}}><td>{{.Label}}</td><td class="muted">{{.Formula}}</td><td class="n">{{.Amount}}</td></tr>
{{end}}</tbody></table>
{{range .Notes}}<p class="muted">Not: {{.}}</p>{{end}}
<p class="muted">Oluşturulma {{.GeneratedAt.Format "2006-01-02 15:04"}} UTC</p>
<button class="noprint" onclick="window.print()">Yazdır / PDF</button>
</body></html>`))

// WriteHTML renders the payslip page.
func (p Payslip) WriteHTML(w io.Writer) error { return payslipHTML.Execute(w, p) }
