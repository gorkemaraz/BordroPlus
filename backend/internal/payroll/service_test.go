package payroll

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/org"
	"bordroplus/internal/secret"
	"bordroplus/internal/seed"
	"bordroplus/internal/testdb"
)

type fixture struct {
	t    *testing.T
	svc  *Service
	pool *pgxpool.Pool
	box  *secret.Box
	emp  int64 // D0001: 65.000 TL from 2026-01-01
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	box, _ := secret.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if _, err := seed.Demo(context.Background(), pool, box, "Demo.Test.2026"); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, svc: NewService(pool, box), pool: pool, box: box}
	if err := pool.QueryRow(context.Background(), `SELECT id FROM employees WHERE employee_no = 'D0001'`).Scan(&f.emp); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) calc(m int) Result {
	f.t.Helper()
	r, err := f.svc.CalculateMonth(context.Background(), f.emp, month(m))
	if err != nil {
		f.t.Fatalf("calculate %d: %v", m, err)
	}
	return r
}

func (f *fixture) salary(from string, amount Kurus) {
	f.t.Helper()
	d, _ := time.Parse("2006-01-02", from)
	if err := org.InsertSalary(context.Background(), f.pool, f.box, nil, f.emp, d, amount); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) stored(m int) (cum, tax Kurus) {
	f.t.Helper()
	if err := f.pool.QueryRow(context.Background(), `
		SELECT cum_tax_base, income_tax FROM payroll_results WHERE employee_id = $1 AND period = $2`,
		f.emp, month(m)).Scan(&cum, &tax); err != nil {
		f.t.Fatal(err)
	}
	return cum, tax
}

func TestDemoEmployeeJanuary(t *testing.T) {
	f := newFixture(t)
	r := f.calc(1)
	if r.RuleVersion != "TR-2026.1" || r.Net != 5093118 || r.BasePay != 6500000 {
		t.Fatalf("January: version %s base %s net %s", r.RuleVersion, r.BasePay, r.Net)
	}
	// Calculating June first computes the missing months it depends on.
	r = f.calc(6)
	if cum, _ := f.stored(5); cum != 5*5525000 || r.CumTaxBase != 6*5525000 {
		t.Fatalf("chain: May cum %s, June cum %s", cum, r.CumTaxBase)
	}
}

// T-3: a raise entered in June, effective from March, recalculates March to
// June; every later month's cumulative base and tax change, and the result
// equals a clean calculation with the raise known from the start.
func TestRetroactiveRaiseRecalculatesLaterMonths(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.calc(6)
	_, juneTaxBefore := f.stored(6)

	f.salary("2026-03-01", 8000000)
	changes, err := f.svc.RecalculateFrom(ctx, f.emp, month(3))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 || changes[0].Period != "2026-03" || changes[3].Period != "2026-06" {
		t.Fatalf("changes: %+v", changes)
	}
	for _, c := range changes {
		if !c.Changed || c.Before == nil || c.Before.CumTaxBase == c.After.CumTaxBase {
			t.Fatalf("month %s not updated: %+v", c.Period, c)
		}
	}
	if cum, _ := f.stored(2); cum != 2*5525000 {
		t.Fatalf("February must not change: %s", cum)
	}

	// Reference: the same year computed from scratch by the pure engine.
	var cum, cumMW Kurus
	var want Result
	for m := 1; m <= 6; m++ {
		salary := Kurus(6500000)
		if m >= 3 {
			salary = 8000000
		}
		want = Calculate(rules2026, full(m, salary, cum, cumMW))
		cum, cumMW = want.CumTaxBase, want.CumMinWageTaxBase
	}
	gotCum, gotTax := f.stored(6)
	if gotCum != want.CumTaxBase || gotTax != want.IncomeTax || gotTax == juneTaxBefore {
		t.Fatalf("June after correction: cum %s tax %s, want %s %s (before %s)", gotCum, gotTax, want.CumTaxBase, want.IncomeTax, juneTaxBefore)
	}

	// Same rule set version, same inputs: a second run changes nothing.
	again, err := f.svc.RecalculateFrom(ctx, f.emp, month(3))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range again {
		if c.Changed || c.Before.Net != c.After.Net {
			t.Fatalf("recalculation not deterministic: %+v", c)
		}
	}
}

func TestMidMonthRaiseAndPartialMonths(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Raise on 16 July: 15 days at 65.000 and 16 days at 80.000 over 31 days.
	f.salary("2026-07-16", 8000000)
	in, c, err := f.svc.BuildInput(ctx, f.pool, f.emp, month(7), 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := Kurus(roundDiv(15*6500000+16*8000000, 31)); in.BasePay != want || in.PaidDays != 30 || in.HourlyBase != 8000000 || len(c.SalaryChanges) != 1 {
		t.Fatalf("mid-month raise: %+v %+v (want base %s)", in, c, want)
	}

	// Hired on 11 May: 21 days of salary ÷ 30.
	var newcomer int64
	err = f.pool.QueryRow(ctx, `
		INSERT INTO employees (employee_no, first_name, last_name, org_unit_id, hire_date)
		VALUES ('E-5001', 'Yeni', 'Çalışan', (SELECT id FROM org_units WHERE code = 'yazilim'), '2026-05-11') RETURNING id`).Scan(&newcomer)
	if err != nil {
		t.Fatal(err)
	}
	if err := org.InsertSalary(ctx, f.pool, f.box, nil, newcomer, time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC), 6000000); err != nil {
		t.Fatal(err)
	}
	in, _, err = f.svc.BuildInput(ctx, f.pool, newcomer, month(5), 0)
	if err != nil || in.PaidDays != 21 || in.BasePay != 4200000 {
		t.Fatalf("partial May: %+v %v", in, err)
	}
	if _, err := f.svc.CalculateMonth(ctx, newcomer, month(4)); !errors.Is(err, ErrNotEmployed) {
		t.Fatalf("before hire: %v", err)
	}
	// May is the first month: no earlier cumulative base.
	r, err := f.svc.CalculateMonth(ctx, newcomer, month(5))
	if err != nil || r.CumTaxBase != r.TaxBase {
		t.Fatalf("first month: %+v %v", r, err)
	}
}

func TestUnpaidLeaveAndApprovedTimesheet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var user int64
	_ = f.pool.QueryRow(ctx, `SELECT id FROM users WHERE email = 'ik@bordroplus.test'`).Scan(&user)

	// Three days of approved unpaid leave in June.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO leave_requests (employee_id, leave_type, start_date, end_date, half_days, status, requested_by)
		VALUES ($1, 'unpaid', '2026-06-08', '2026-06-10', 6, 'approved', $2)`, f.emp, user); err != nil {
		t.Fatal(err)
	}
	in, c, err := f.svc.BuildInput(ctx, f.pool, f.emp, month(6), 0)
	if err != nil || in.PaidDays != 27 || in.BasePay != 5850000 || c.UnpaidDays != 3 || c.TimesheetStatus != "not_approved" {
		t.Fatalf("unpaid leave: %+v %+v %v", in, c, err)
	}

	// An approved timesheet month brings its overtime in.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO timesheet_periods (employee_id, month, rule_version, totals, approved_by)
		VALUES ($1, '2026-06-01', 'TS-2026.1',
		        '{"totals": {"overtime_minutes": 600, "rest_day_minutes": 0, "holiday_minutes": 0},
		          "multipliers_pct": {"overtime": 150, "rest_day": 150, "holiday": 200}}', $2)`, f.emp, user); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.CalculateMonth(ctx, f.emp, month(6))
	// 65.000 ÷ 225 × 10 h × 1.5 = 4.333,33
	if err != nil || r.OvertimePay != 433333 || r.Gross != 5850000+433333 {
		t.Fatalf("overtime from timesheet: %+v %v", r, err)
	}
}

// Each period uses the rule set valid then, and the cumulative base starts
// again in January.
func TestRuleSetByPeriodAndYearBoundary(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.salary("2025-12-01", 6000000)

	dec, err := f.svc.CalculateMonth(ctx, f.emp, time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || dec.RuleVersion != "TR-2025.1" {
		t.Fatalf("December 2025: %+v %v", dec, err)
	}
	jan := f.calc(1)
	if jan.RuleVersion != "TR-2026.1" || jan.CumTaxBase != jan.TaxBase {
		t.Fatalf("January 2026 must start a new cumulative base: %+v", jan)
	}
	if _, err := f.svc.CalculateMonth(ctx, f.emp, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrNoRuleSet) {
		t.Fatalf("no rule set: %v", err)
	}
}
