package timesheet

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/secret"
	"bordroplus/internal/seed"
	"bordroplus/internal/testdb"
)

// Clock frozen at 2 Dec 2026: the November payroll month (26 Oct - 29 Nov)
// has ended, December is open.
type fixture struct {
	t    *testing.T
	svc  *Service
	pool *pgxpool.Pool
	hr   Actor
	emp  int64 // D0001
}

var november = day("2026-11-01")

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	box, _ := secret.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if _, err := seed.Demo(context.Background(), pool, box, "Demo.Test.2026"); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool)
	svc.now = func() time.Time { return at("2026-12-02 12:00") }
	f := &fixture{t: t, svc: svc, pool: pool}
	var hrUser int64
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email = 'ik@bordroplus.test'`).Scan(&hrUser); err != nil {
		t.Fatal(err)
	}
	f.hr = Actor{UserID: hrUser, Role: "hr"}
	if err := pool.QueryRow(ctx, `SELECT id FROM employees WHERE employee_no = 'D0001'`).Scan(&f.emp); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) punch(spec ...string) {
	f.t.Helper()
	var ps []NewPunch
	for _, p := range punches(spec...) {
		ps = append(ps, NewPunch{EmployeeID: f.emp, At: p.At, Dir: p.Dir, Source: "device", DeviceID: "T1"})
	}
	if _, _, err := f.svc.Import(context.Background(), f.hr, ps); err != nil {
		f.t.Fatalf("import: %v", err)
	}
}

func (f *fixture) month(m time.Time) MonthSummary {
	f.t.Helper()
	s, err := f.svc.Month(context.Background(), f.emp, m)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fixture) proposal(key string) Correction {
	f.t.Helper()
	kind, when, _ := strings.Cut(key, " ") // "add_missing_out 2026-11-05 08:00"
	var id int64
	err := f.pool.QueryRow(context.Background(), `
		SELECT c.id FROM punch_corrections c JOIN time_punches p ON p.id = c.target_punch_id
		 WHERE c.employee_id = $1 AND c.kind = $2 AND p.punched_at = $3`, f.emp, kind, at(when)).Scan(&id)
	if err != nil {
		f.t.Fatalf("proposal %s: %v", key, err)
	}
	c, err := f.svc.GetCorrection(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

func allow(Correction) error { return nil }

// T-2 end to end: inconsistent punches produce proposals, nothing changes
// until they are approved, the month cannot be approved before that, and
// an approved month is frozen.
func TestCorrectionsThenMonthApproval(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.punch(
		"in 2026-11-02 08:00", "out 2026-11-02 17:00", // Mon clean: 540 gross, 480 net
		"in 2026-11-03 08:00", "out 2026-11-03 17:00", // Tue clean
		"in 2026-11-04 08:00", "in 2026-11-04 08:01", "out 2026-11-04 17:00", // Wed double punch
		"in 2026-11-05 08:00",                         // Thu: no out
		"in 2026-11-06 08:00", "out 2026-11-06 17:00", // Fri clean
	)

	s := f.month(november)
	if s.OpenAnomalies != 2 || s.Totals.NormalMinutes != 4*480 {
		t.Fatalf("before corrections: %+v", s) // Wed counts (first in kept), Thu does not
	}
	if _, err := f.svc.ApproveMonth(ctx, f.hr, f.emp, november); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("approve with anomalies: %v", err)
	}

	// Raw punches are untouched by the proposals.
	var raw int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM time_punches WHERE employee_id = $1`, f.emp).Scan(&raw)
	if raw != 10 {
		t.Fatalf("raw punches = %d", raw)
	}

	dup := f.proposal("drop_duplicate 2026-11-04 08:01")
	miss := f.proposal("add_missing_out 2026-11-05 08:00")
	if miss.ProposedAt == nil || !miss.ProposedAt.Equal(at("2026-11-05 17:00")) { // typical shift: 9 h
		t.Fatalf("suggested out: %+v", miss.ProposedAt)
	}
	if _, err := f.svc.DecideCorrection(ctx, f.hr, dup.ID, true, nil, "", allow); err != nil {
		t.Fatal(err)
	}
	override := at("2026-11-05 18:30")
	c, err := f.svc.DecideCorrection(ctx, f.hr, miss.ID, true, &override, "vardiya uzadı", allow)
	if err != nil || c.Status != "approved" || !c.AppliedAt.Equal(override) {
		t.Fatalf("approve missing out: %+v %v", c, err)
	}

	s = f.month(november)
	// Thu 08:00-18:30 = 630 gross, 570 net.
	if s.OpenAnomalies != 0 || s.Totals.NormalMinutes != 4*480+570 || s.Totals.WorkedDays != 5 {
		t.Fatalf("after corrections: %+v", s)
	}
	approved, err := f.svc.ApproveMonth(ctx, f.hr, f.emp, november)
	if err != nil || approved.Status != "approved" {
		t.Fatalf("approve month: %+v %v", approved, err)
	}

	// Frozen: no second approval, no new punches, no corrections in that month.
	if _, err := f.svc.ApproveMonth(ctx, f.hr, f.emp, november); !errors.Is(err, ErrPeriodLocked) {
		t.Fatalf("second approval: %v", err)
	}
	if _, _, err := f.svc.Import(ctx, f.hr, []NewPunch{{EmployeeID: f.emp, At: at("2026-11-10 08:00"), Dir: In, Source: "manual"}}); !errors.Is(err, ErrPeriodLocked) {
		t.Fatalf("punch into approved month: %v", err)
	}
	if got := f.month(november); got.Status != "approved" || got.Totals != approved.Totals {
		t.Fatalf("frozen snapshot: %+v", got)
	}
	// December is still open and cannot be approved before it ends.
	if _, err := f.svc.ApproveMonth(ctx, f.hr, f.emp, day("2026-12-01")); !errors.Is(err, ErrPeriodOpen) {
		t.Fatalf("approve running month: %v", err)
	}
}

// A proposal overtaken by a real punch becomes obsolete and cannot be applied.
func TestProposalObsoletedByManualPunch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.punch("in 2026-11-09 08:00", "in 2026-11-10 08:00", "out 2026-11-10 17:00")
	f.month(november)
	p := f.proposal("add_missing_out 2026-11-09 08:00")

	if _, _, err := f.svc.Import(ctx, f.hr, []NewPunch{{EmployeeID: f.emp, At: at("2026-11-09 17:45"), Dir: Out, Source: "manual"}}); err != nil {
		t.Fatal(err)
	}
	if s := f.month(november); s.OpenAnomalies != 0 {
		t.Fatalf("anomaly still open: %+v", s)
	}
	if _, err := f.svc.DecideCorrection(ctx, f.hr, p.ID, true, nil, "", allow); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("applying an obsolete proposal: %v", err)
	}
	if c, _ := f.svc.GetCorrection(ctx, p.ID); c.Status != "obsolete" {
		t.Fatalf("status = %s", c.Status)
	}
}

// Five approvers click "approve" on the same proposal at once: one wins and
// exactly one correction punch is written.
func TestConcurrentCorrectionApproval(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.punch("in 2026-11-11 08:00", "in 2026-11-12 08:00", "out 2026-11-12 17:00")
	f.month(november)
	p := f.proposal("add_missing_out 2026-11-11 08:00")

	var wg sync.WaitGroup
	results := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.DecideCorrection(ctx, f.hr, p.ID, true, nil, "", allow)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyDecided):
			conflicts++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	var added int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM time_punches WHERE correction_id = $1`, p.ID).Scan(&added)
	if ok != 1 || conflicts != 4 || added != 1 {
		t.Fatalf("ok=%d conflicts=%d correction punches=%d", ok, conflicts, added)
	}
}

func TestDeviceRetryIsIdempotent(t *testing.T) {
	f := newFixture(t)
	p := NewPunch{EmployeeID: f.emp, At: at("2026-11-16 08:00"), Dir: In, Source: "device", DeviceID: "T1"}
	ins, dup, err := f.svc.Import(context.Background(), f.hr, []NewPunch{p, p})
	if err != nil || ins != 1 || dup != 1 {
		t.Fatalf("inserted=%d duplicates=%d err=%v", ins, dup, err)
	}
}

// A night shift from Sunday 29 Nov into Monday 30 Nov is split across two
// payroll months: the Sunday part is November rest-day work, the Monday
// part December ordinary time.
func TestNightShiftAcrossMonthBoundary(t *testing.T) {
	f := newFixture(t)
	f.punch("in 2026-11-29 22:00", "out 2026-11-30 06:00")
	nov, dec := f.month(november), f.month(day("2026-12-01"))
	if nov.Totals.RestDayMinutes != 120 || nov.Totals.NormalMinutes != 0 {
		t.Fatalf("November: %+v", nov.Totals)
	}
	if dec.Totals.NormalMinutes != 300 { // 360 - 60 break taken at 02:00 Monday
		t.Fatalf("December: %+v", dec.Totals)
	}
}

func TestMonthlyOvertime(t *testing.T) {
	f := newFixture(t)
	var spec []string
	for _, d := range []string{"16", "17", "18", "19", "20", "21"} { // Mon-Sat 9.5 h
		spec = append(spec, "in 2026-11-"+d+" 08:00", "out 2026-11-"+d+" 17:30")
	}
	f.punch(spec...)
	s := f.month(november)
	if s.Totals.NormalMinutes != 2700 || s.Totals.OvertimeMinutes != 360 || s.YearOvertime != 360 || s.OvertimeCapHit {
		t.Fatalf("overtime: %+v", s)
	}
	if s.Multipliers.Overtime != 150 || s.Multipliers.Holiday != 200 || s.RuleVersion != "TS-2026.1" {
		t.Fatalf("rules: %+v %s", s.Multipliers, s.RuleVersion)
	}
}
