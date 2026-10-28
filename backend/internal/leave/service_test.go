package leave

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/secret"
	"bordroplus/internal/seed"
	"bordroplus/internal/testdb"
)

// fixture is the demo data set with the clock frozen at 28 Oct 2026 12:00.
// Calendar notes: 2 Nov 2026 is a Monday; there are no public holidays in
// November and December 2026.
type fixture struct {
	t    *testing.T
	svc  *Service
	pool *pgxpool.Pool
	hr   Actor // approver (İK)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	box, err := secret.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Demo(context.Background(), pool, box, "Demo.Test.2026"); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool)
	svc.now = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, istanbul) }
	f := &fixture{t: t, svc: svc, pool: pool}
	f.hr = Actor{UserID: f.userID("ik"), Role: "hr"}
	t.Cleanup(f.checkLedgerInvariant)
	return f
}

func (f *fixture) emp(no string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM employees WHERE employee_no = $1`, no).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) userID(local string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, local+"@bordroplus.test").Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) balance(emp int64) Balance {
	f.t.Helper()
	b, err := f.svc.GetBalance(context.Background(), emp)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *fixture) request(emp int64, typ, start, end string, half HalfDay) (Request, error) {
	return f.svc.Create(context.Background(), f.hr, NewRequest{EmployeeID: emp, Type: typ, Start: d(start), End: d(end), Half: half})
}

func (f *fixture) mustRequest(emp int64, typ, start, end string) Request {
	f.t.Helper()
	r, err := f.request(emp, typ, start, end, FullDay)
	if err != nil {
		f.t.Fatalf("create %s %s..%s: %v", typ, start, end, err)
	}
	return r
}

func allow(Request) error { return nil }

func (f *fixture) mustApprove(id int64) Request {
	f.t.Helper()
	r, err := f.svc.Approve(context.Background(), f.hr, id, "", allow)
	if err != nil {
		f.t.Fatalf("approve %d: %v", id, err)
	}
	return r
}

// checkLedgerInvariant: every account balance equals the sum of its ledger
// and is never negative.
func (f *fixture) checkLedgerInvariant() {
	var bad int
	err := f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM leave_accounts a
		 WHERE a.balance_half_days <> (SELECT COALESCE(sum(half_days), 0) FROM leave_ledger l WHERE l.employee_id = a.employee_id)
		    OR a.balance_half_days < 0`).Scan(&bad)
	if err != nil || bad != 0 {
		f.t.Errorf("ledger invariant broken for %d accounts (%v)", bad, err)
	}
}

// T-1: balances of the demo employees match a hand calculation.
func TestDemoBalances(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		no                      string
		years                   int
		total, carried, current float64
		nextDate                string
		nextDays                float64
	}{
		// Hired 2024-03-01, born 2006-09-01: aged 18 at the 1st anniversary
		// (20-day floor), 19 at the 2nd (14 days).
		{"D0001", 2, 34, 20, 14, "2027-03-01", 14},
		// Hired 2021-06-01, born 1975-05-20: 14+14+14, then aged 50 and 51 → 20+20.
		{"D0004", 5, 82, 62, 20, "2027-06-01", 20},
		// Hired 2010-01-04: years 1-5 ×14, 6-14 ×20, 15-16 ×26.
		{"D0005", 16, 302, 276, 26, "2027-01-04", 26},
		// Hired 2022-11-21: three anniversaries ×14; the 4th is next month.
		{"D0006", 3, 42, 28, 14, "2026-11-21", 14},
	}
	for _, c := range cases {
		b := f.balance(f.emp(c.no))
		if b.ServiceYears != c.years || b.TotalAccrued != c.total || b.Balance != c.total ||
			b.CarriedOver != c.carried || b.CurrentAccrual != c.current ||
			b.NextAccrual == nil || b.NextAccrual.Date != c.nextDate || b.NextAccrual.Days != c.nextDays {
			t.Errorf("%s: %+v (next %+v)", c.no, b, b.NextAccrual)
		}
	}
	// Reading again books nothing twice.
	if b := f.balance(f.emp("D0001")); b.TotalAccrued != 34 {
		t.Fatalf("accruals booked twice: %v", b.TotalAccrued)
	}
}

func TestAnnualLeaveLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	emp := f.emp("D0001") // 34 days

	r := f.mustRequest(emp, TypeAnnual, "2026-11-02", "2026-11-13") // 10 working days
	if r.Days != 10 || r.Status != "pending" {
		t.Fatalf("request = %+v", r)
	}
	if b := f.balance(emp); b.Balance != 34 || b.Pending != 10 || b.Available != 24 {
		t.Fatalf("after request: %+v", b)
	}
	// New requests are limited by what is available, not by the booked balance.
	if _, err := f.request(emp, TypeAnnual, "2026-11-16", "2026-12-18", FullDay); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("25 days with 24 available: %v", err)
	}

	f.mustApprove(r.ID)
	if b := f.balance(emp); b.Balance != 24 || b.Pending != 0 || b.TotalUsed != 10 {
		t.Fatalf("after approval: %+v", b)
	}
	if _, err := f.svc.Approve(ctx, f.hr, r.ID, "", allow); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second approval: %v", err)
	}

	// HR cancels the approved leave: the days come back.
	if _, err := f.svc.Cancel(ctx, f.hr, r.ID, allow); err != nil {
		t.Fatal(err)
	}
	if b := f.balance(emp); b.Balance != 34 || b.TotalRefunded != 10 {
		t.Fatalf("after cancellation: %+v", b)
	}

	// Rejected requests never touch the balance.
	r2 := f.mustRequest(emp, TypeAnnual, "2026-11-16", "2026-11-20")
	if _, err := f.svc.Reject(ctx, f.hr, r2.ID, "yoğun dönem", allow); err != nil {
		t.Fatal(err)
	}
	if b := f.balance(emp); b.Balance != 34 || b.Pending != 0 {
		t.Fatalf("after rejection: %+v", b)
	}
}

// T-1 concurrency: five pending requests of 10 days each are approved at the
// same time for an employee with 34 days. Exactly three fit; the balance
// ends at 4 days and never goes negative.
func TestConcurrentApprovalsNeverOverdraw(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	emp := f.emp("D0001")
	if b := f.balance(emp); b.Balance != 34 {
		t.Fatalf("starting balance %v", b.Balance)
	}

	// Inserted directly: Create would already refuse the 4th (pending counts).
	// This is the state after, e.g., HR corrected an accrual downwards.
	ranges := [][2]string{
		{"2026-11-02", "2026-11-13"}, {"2026-11-16", "2026-11-27"}, {"2026-11-30", "2026-12-11"},
		{"2026-12-14", "2026-12-25"}, {"2027-01-11", "2027-01-22"},
	}
	var ids []int64
	for _, rg := range ranges {
		var id int64
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO leave_requests (employee_id, leave_type, start_date, end_date, half_days, requested_by)
			VALUES ($1, 'annual', $2, $3, 20, $4) RETURNING id`, emp, rg[0], rg[1], f.hr.UserID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	var (
		wg                sync.WaitGroup
		mu                sync.Mutex
		approved, refused int
		start             = make(chan struct{})
	)
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.svc.Approve(ctx, f.hr, id, "", allow)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				approved++
			case errors.Is(err, ErrInsufficientBalance):
				refused++
			default:
				t.Errorf("approve %d: %v", id, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if approved != 3 || refused != 2 {
		t.Fatalf("approved=%d refused=%d, want 3 and 2", approved, refused)
	}
	if b := f.balance(emp); b.Balance != 4 {
		t.Fatalf("final balance %v, want 4", b.Balance)
	}
}

// The database CHECK holds even if application code tried to overdraw.
func TestBalanceCannotGoNegativeInDatabase(t *testing.T) {
	f := newFixture(t)
	emp := f.emp("D0001")
	f.balance(emp)
	_, err := f.pool.Exec(context.Background(), `UPDATE leave_accounts SET balance_half_days = -1 WHERE employee_id = $1`, emp)
	if err == nil {
		t.Fatal("negative balance accepted by the database")
	}
}

// T-1: sick report days during annual leave are not consumed, whichever of
// the two is approved first.
func TestSickReportDuringLeaveIsRefunded(t *testing.T) {
	f := newFixture(t)
	emp := f.emp("D0001") // 34 days

	// Annual first, then the report (Wed-Fri inside the leave).
	a1 := f.mustRequest(emp, TypeAnnual, "2026-11-02", "2026-11-13")
	f.mustApprove(a1.ID)
	s1 := f.mustRequest(emp, TypeSick, "2026-11-04", "2026-11-06")
	f.mustApprove(s1.ID)
	if b := f.balance(emp); b.Balance != 27 || b.TotalRefunded != 3 {
		t.Fatalf("annual then sick: %+v", b) // 34 - 10 + 3
	}

	// Report first, then annual leave covering it (sick reports may overlap leave).
	s2 := f.mustRequest(emp, TypeSick, "2026-11-16", "2026-11-18")
	f.mustApprove(s2.ID)
	a2 := f.mustRequest(emp, TypeAnnual, "2026-11-16", "2026-11-27")
	f.mustApprove(a2.ID)
	if b := f.balance(emp); b.Balance != 20 || b.TotalRefunded != 6 {
		t.Fatalf("sick then annual: %+v", b) // 27 - 10 + 3
	}

	// Approving something unrelated re-runs the refund pass: nothing is paid twice.
	a3 := f.mustRequest(emp, TypeAnnual, "2026-12-07", "2026-12-07")
	f.mustApprove(a3.ID)
	if b := f.balance(emp); b.Balance != 19 || b.TotalRefunded != 6 {
		t.Fatalf("after unrelated approval: %+v", b)
	}

	// Cancelling a leave that was partly refunded reverses only what is still charged.
	if _, err := f.svc.Cancel(context.Background(), f.hr, a1.ID, allow); err != nil {
		t.Fatal(err)
	}
	if b := f.balance(emp); b.Balance != 26 {
		t.Fatalf("after cancelling a1: %+v", b) // + (10 - 3)
	}
	// An approved sick report cannot be cancelled: refunds depend on it.
	if _, err := f.svc.Cancel(context.Background(), f.hr, s2.ID, allow); !errors.Is(err, ErrNotPending) {
		t.Fatalf("cancel approved sick: %v", err)
	}
}

func TestHalfDaysHolidaysAndOverlaps(t *testing.T) {
	f := newFixture(t)
	emp := f.emp("D0006") // 42 days

	// 28 Oct 2026 is Cumhuriyet Bayramı arifesi (afternoon off), 29 Oct the holiday.
	r, err := f.request(emp, TypeAnnual, "2026-10-28", "2026-10-28", Morning)
	if err != nil || r.Days != 0.5 {
		t.Fatalf("morning on arife: %+v %v", r, err)
	}
	if _, err := f.request(emp, TypeAnnual, "2026-11-03", "2026-11-03", Afternoon); err != nil {
		t.Fatalf("afternoon on a normal day: %v", err)
	}
	if _, err := f.request(emp, TypeAnnual, "2026-10-29", "2026-10-29", FullDay); !errors.Is(err, ErrNoWorkingDays) {
		t.Fatalf("leave on a holiday: %v", err)
	}
	if _, err := f.request(emp, TypeAnnual, "2026-10-31", "2026-11-01", FullDay); !errors.Is(err, ErrNoWorkingDays) {
		t.Fatalf("leave on a weekend: %v", err)
	}
	// 26 Oct - 30 Oct: Mon, Tue, Wed half (arife), Thu holiday, Fri = 3.5 days,
	// but it overlaps the pending morning leave on the 28th.
	if _, err := f.request(emp, TypeAnnual, "2026-10-26", "2026-10-30", FullDay); !errors.Is(err, ErrOverlap) {
		t.Fatalf("overlap: %v", err)
	}
	r2, err := f.request(emp, TypeUnpaid, "2026-11-23", "2026-11-27", FullDay)
	if err != nil || r2.Days != 5 {
		t.Fatalf("unpaid: %+v %v", r2, err)
	}
	f.mustApprove(r2.ID)
	if b := f.balance(emp); b.Balance != 42 {
		t.Fatalf("unpaid leave must not touch the balance: %v", b.Balance)
	}
}
