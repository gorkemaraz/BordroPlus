package payroll

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/db"
	"bordroplus/internal/org"
)

const workers = 16

func (f *fixture) actor(local, role string) Actor {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, local+"@bordroplus.test").Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return Actor{UserID: id, Role: role}
}

func (f *fixture) run(m int) RunReport {
	f.t.Helper()
	rep, err := f.svc.RunPeriod(context.Background(), f.actor("bordro", "payroll"), month(m), workers)
	if err != nil || len(rep.Failures) > 0 {
		f.t.Fatalf("run %d: %+v %v", m, rep, err)
	}
	return rep
}

// closeMonth runs a month through submit → approve → close.
func (f *fixture) closeMonth(m int) {
	f.t.Helper()
	ctx := context.Background()
	payroll, accounting := f.actor("bordro", "payroll"), f.actor("muhasebe", "accounting")
	for _, step := range []struct {
		a      Actor
		action string
	}{{payroll, "submit"}, {accounting, "approve"}, {accounting, "close"}} {
		if _, err := f.svc.Transition(ctx, step.a, month(m), step.action, ""); err != nil {
			f.t.Fatalf("%s %d: %v", step.action, m, err)
		}
	}
}

// T-4: the state machine, separation of duties and frozen results.
func TestPeriodWorkflowAndFrozenResults(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	payroll, accounting := f.actor("bordro", "payroll"), f.actor("muhasebe", "accounting")

	if _, err := f.svc.Transition(ctx, payroll, month(1), "submit", ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("submit before any run: %v", err)
	}
	rep := f.run(1)
	if rep.Employees != 6 || rep.Changed != 6 {
		t.Fatalf("run: %+v", rep)
	}
	// February cannot start while January is open.
	if _, err := f.svc.RunPeriod(ctx, payroll, month(2), workers); !errors.Is(err, ErrPreviousOpen) {
		t.Fatalf("February before January closed: %v", err)
	}

	if _, err := f.svc.Transition(ctx, payroll, month(1), "approve", ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("approve a draft: %v", err)
	}
	if _, err := f.svc.Transition(ctx, payroll, month(1), "submit", ""); err != nil {
		t.Fatal(err)
	}
	// Under review the results are frozen, for the API and for raw SQL alike.
	if _, err := f.svc.CalculateMonth(ctx, f.emp, month(1)); !errors.Is(err, ErrPeriodFrozen) {
		t.Fatalf("recalculate under review: %v", err)
	}
	if _, err := f.svc.RunPeriod(ctx, payroll, month(1), workers); !errors.Is(err, ErrPeriodFrozen) {
		t.Fatalf("re-run under review: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE payroll_results SET net = 0 WHERE period = '2026-01-01'`); db.PgCode(err) != "55000" {
		t.Fatalf("raw UPDATE of frozen results: %v", err)
	}

	// Rejection sends it back to draft, where it can be recalculated.
	if p, err := f.svc.Transition(ctx, accounting, month(1), "reject", "SGK günleri kontrol edilsin"); err != nil || p.Status != StatusDraft {
		t.Fatalf("reject: %+v %v", p, err)
	}
	if _, err := f.svc.CalculateMonth(ctx, f.emp, month(1)); err != nil {
		t.Fatalf("recalculate after rejection: %v", err)
	}
	if _, err := f.svc.Transition(ctx, payroll, month(1), "submit", ""); err != nil {
		t.Fatal(err)
	}
	// Whoever submitted cannot approve (here: the same user acting as approver).
	if _, err := f.svc.Transition(ctx, payroll, month(1), "approve", ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("submitter approving: %v", err)
	}
	for _, action := range []string{"approve", "close"} {
		if _, err := f.svc.Transition(ctx, accounting, month(1), action, ""); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}

	// Closed for good.
	if _, err := f.pool.Exec(ctx, `UPDATE payroll_periods SET status = 'draft' WHERE period = '2026-01-01'`); db.PgCode(err) != "55000" {
		t.Fatalf("reopening a closed period: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM payroll_results WHERE period = '2026-01-01'`); db.PgCode(err) != "55000" {
		t.Fatalf("deleting closed results: %v", err)
	}
	if _, err := f.svc.RecalculateFrom(ctx, f.emp, month(1)); !errors.Is(err, ErrPeriodFrozen) {
		t.Fatalf("recalculating a closed month: %v", err)
	}
	f.run(2) // now February may start
}

// T-4: the same rule set version gives the same payroll.
func TestRerunIsDeterministic(t *testing.T) {
	f := newFixture(t)
	first := f.run(1)
	second := f.run(1)
	if second.Changed != 0 || second.Unchanged != 6 || second.Digest != first.Digest || second.Totals != first.Totals {
		t.Fatalf("re-run changed the payroll:\n%+v\n%+v", first, second)
	}
}

// T-4: a correction for a closed month is paid in the next open payroll as
// a prior-period line; the closed month itself is untouched, and the
// difference is never paid twice.
func TestPriorPeriodCorrection(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.run(1)
	f.closeMonth(1)
	before, _, err := f.svc.StoredResult(ctx, f.emp, month(1))
	if err != nil {
		t.Fatal(err)
	}

	// After January is closed, HR enters a raise effective 15 January.
	f.salary("2026-01-15", 8000000)

	f.run(2)
	jan, _, _ := f.svc.StoredResult(ctx, f.emp, month(1))
	if jan.Net != before.Net || jan.Gross != before.Gross {
		t.Fatal("closed January changed")
	}
	feb, _, _ := f.svc.StoredResult(ctx, f.emp, month(2))
	// January with the raise: (14 × 65.000 + 17 × 80.000) / 31 = 73.225,81.
	wantJan := Kurus(roundDiv(14*6500000+17*8000000, 31))
	var retro *Line
	for i, l := range feb.Lines {
		if l.Code == "retro_2026-01" {
			retro = &feb.Lines[i]
		}
	}
	if retro == nil || retro.Amount != wantJan-6500000 {
		t.Fatalf("February correction line: %+v (want %s)", retro, wantJan-6500000)
	}
	if feb.Gross != 8000000+retro.Amount || feb.CumTaxBase != before.CumTaxBase+feb.TaxBase {
		t.Fatalf("February: gross %s cum %s", feb.Gross, feb.CumTaxBase)
	}

	// Once paid in (closed) February, March does not pay it again.
	f.closeMonth(2)
	f.run(3)
	mar, _, _ := f.svc.StoredResult(ctx, f.emp, month(3))
	for _, l := range mar.Lines {
		if strings.HasPrefix(l.Code, retroPrefix) {
			t.Fatalf("correction paid twice: %+v", l)
		}
	}
	var n int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM payroll_adjustments WHERE employee_id = $1`, f.emp).Scan(&n)
	if n != 1 {
		t.Fatalf("adjustments = %d, want 1", n)
	}
}

// T-4 performance: 1.500 employees are calculated in parallel well inside
// 10 seconds, and a second run with the same rules gives the same digest.
func TestRun1500EmployeesUnder10Seconds(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	f := newFixture(t)
	ctx := context.Background()
	const n = 1500
	rows, err := f.pool.Query(ctx, `
		INSERT INTO employees (employee_no, first_name, last_name, org_unit_id, hire_date)
		SELECT 'P' || lpad(g::text, 5, '0'), 'Ad' || g, 'Soyad' || g, (SELECT id FROM org_units WHERE code = 'yazilim'),
		       DATE '2015-01-01' + (g % 3000)
		  FROM generate_series(1, $1) g RETURNING id`, n)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	var copyRows [][]any
	for i, id := range ids {
		amount := Kurus(3303000 + int64(i%400)*50000) // 33.030 to ~233.000 TL
		enc, err := f.box.SealInt64(int64(amount), org.SalaryAAD(id, from))
		if err != nil {
			t.Fatal(err)
		}
		copyRows = append(copyRows, []any{id, from, enc, int16(1)})
	}
	if _, err := f.pool.CopyFrom(ctx, pgx.Identifier{"employee_salaries"},
		[]string{"employee_id", "valid_from", "amount_enc", "key_version"}, pgx.CopyFromRows(copyRows)); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	rep := f.run(1)
	elapsed := time.Since(start)
	t.Logf("%d employees, %d workers: %s (%d ms reported), employer cost %s", rep.Employees, rep.Workers, elapsed, rep.DurationMS, rep.Totals.EmployerCost)
	if rep.Employees != n+6 || elapsed > 10*time.Second {
		t.Fatalf("run: %d employees in %s", rep.Employees, elapsed)
	}
	again := f.run(1)
	if again.Digest != rep.Digest || again.Changed != 0 {
		t.Fatalf("second run differs: changed %d", again.Changed)
	}
	fmt.Printf("payroll run: %d employees in %s, re-run %d ms, digest %s\n", rep.Employees, elapsed.Round(time.Millisecond), again.DurationMS, rep.Digest[:16])
}

// A hire entered after January closed, with a January hire date: January's
// payroll stays closed, February skips it in the cumulative chain and pays
// January's salary as a prior-period correction.
func TestBackdatedHireAfterClose(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.run(1)
	f.closeMonth(1)

	var late int64
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO employees (employee_no, first_name, last_name, org_unit_id, hire_date)
		VALUES ('L-1', 'Geç', 'Giriş', (SELECT id FROM org_units WHERE code = 'yazilim'), '2026-01-05') RETURNING id`).Scan(&late); err != nil {
		t.Fatal(err)
	}
	if err := org.InsertSalary(ctx, f.pool, f.box, nil, late, month(1).AddDate(0, 0, 4), 6000000); err != nil {
		t.Fatal(err)
	}
	rep := f.run(2)
	if rep.Employees != 7 {
		t.Fatalf("employees in February: %d", rep.Employees)
	}
	feb, _, err := f.svc.StoredResult(ctx, late, month(2))
	if err != nil {
		t.Fatal(err)
	}
	// 5-31 January: 27 days × 60.000 / 30 = 54.000.
	var retro Kurus
	for _, l := range feb.Lines {
		if l.Code == "retro_2026-01" {
			retro = l.Amount
		}
	}
	if retro != 5400000 || feb.CumTaxBase != feb.TaxBase {
		t.Fatalf("February of the late hire: retro %s, cum %s, base %s", retro, feb.CumTaxBase, feb.TaxBase)
	}
}
