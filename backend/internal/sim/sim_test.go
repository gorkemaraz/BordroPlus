package sim

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"bordroplus/internal/leave"
	"bordroplus/internal/secret"
	"bordroplus/internal/seed"
	"bordroplus/internal/testdb"
	"bordroplus/internal/timesheet"
)

var (
	hFrom = date(2024, 10, 1)
	hTo   = date(2026, 10, 27)
)

func TestSameSeedSameData(t *testing.T) {
	a, b := People(42, 300, hFrom, hTo), People(42, 300, hFrom, hTo)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different people")
	}
	if reflect.DeepEqual(a, People(43, 300, hFrom, hTo)) {
		t.Fatal("different seeds produced the same people")
	}
	d := date(2026, 3, 10)
	if !reflect.DeepEqual(Day(42, a[7], d, nil), Day(42, b[7], d, nil)) {
		t.Fatal("same seed produced different punches")
	}
	if !reflect.DeepEqual(Absences(42, a[7], hFrom, hTo, nil), Absences(42, b[7], hFrom, hTo, nil)) {
		t.Fatal("same seed produced different leave")
	}
}

func TestPeopleAreValid(t *testing.T) {
	units := map[string]int{}
	for _, p := range People(7, 1500, hFrom, hTo) {
		units[p.Unit]++
		if !p.Birth.Before(p.Hire) || p.Salary2026 < 3303000 || p.Salary2025 < 2600550 || p.Salary2025 > p.Salary2026 {
			t.Fatalf("invalid person %+v", p)
		}
		if p.Term != nil && p.Term.Before(p.Hire) {
			t.Fatalf("terminated before hire: %+v", p)
		}
	}
	if len(units) != len(Units) || units["cagri_merkezi"] < units["hazine"] {
		t.Fatalf("unit distribution: %v", units)
	}
}

func TestAbsencesNeverOverlapOrOverdraw(t *testing.T) {
	for _, p := range People(9, 400, hFrom, hTo) {
		abs := Absences(9, p, hFrom, hTo, nil)
		used := 0
		for i, a := range abs {
			if i > 0 && !a.Start.After(abs[i-1].End) {
				t.Fatalf("overlap for %s: %+v / %+v", p.No, abs[i-1], a)
			}
			if a.Type != leave.TypeAnnual {
				continue
			}
			earned := 0
			for _, x := range leave.Accruals(p.Hire, &p.Birth, p.Term, hTo) {
				if !x.Date.After(a.Start) {
					earned += x.HalfDays
				}
			}
			used += a.HalfDays
			if used > earned {
				t.Fatalf("%s overdraws on %s: used %d earned %d", p.No, a.Start.Format("2006-01-02"), used, earned)
			}
		}
	}
}

// The deliberate faults appear at roughly the planned rates, and pairing
// detects them.
func TestPunchFaultRates(t *testing.T) {
	people := People(11, 200, hFrom, hTo)
	var shifts, anomalies int
	for _, p := range people {
		var ps []timesheet.Punch
		id := int64(0)
		for d := date(2025, 1, 6); d.Before(date(2025, 7, 1)); d = d.AddDate(0, 0, 1) {
			for _, e := range Day(11, p, d, nil) {
				id++
				ps = append(ps, timesheet.Punch{ID: id, At: e.At, Dir: timesheet.Direction(e.Dir)})
				if e.Dir == "in" {
					shifts++
				}
			}
		}
		_, a := timesheet.Pair(ps, date(2025, 8, 1), timesheet.DefaultRules)
		anomalies += len(a)
	}
	rate := float64(anomalies) / float64(shifts)
	if rate < 0.015 || rate > 0.045 {
		t.Fatalf("anomaly rate %.3f over %d shifts", rate, shifts)
	}
}

func TestLoadIsIdempotentAndLiveTicksOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("database load")
	}
	pool := testdb.New(t)
	ctx := context.Background()
	box, _ := secret.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if _, err := seed.Demo(ctx, pool, box, "Demo.Test.2026"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{Seed: 42, Employees: 80, Today: date(2026, 10, 28)}

	st, err := Load(ctx, pool, box, cfg, log)
	if err != nil || st.Skipped || st.Employees != 80 || st.Punches < 20000 {
		t.Fatalf("load: %+v %v", st, err)
	}
	again, err := Load(ctx, pool, box, cfg, log)
	if err != nil || !again.Skipped {
		t.Fatalf("second load: %+v %v", again, err)
	}

	var bad int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM leave_accounts a
		 WHERE a.balance_half_days <> (SELECT COALESCE(sum(half_days), 0) FROM leave_ledger l WHERE l.employee_id = a.employee_id)`).
		Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("ledger invariant: %d %v", bad, err)
	}
	var unmanaged int
	_ = pool.QueryRow(ctx, `
		SELECT count(*) FROM org_units u
		 WHERE u.manager_id IS NULL AND EXISTS (SELECT 1 FROM employees e WHERE e.org_unit_id = u.id)`).Scan(&unmanaged)
	if unmanaged != 0 {
		t.Fatalf("%d staffed units without a manager", unmanaged)
	}

	// Live mode: noon on the 28th inserts the morning in-punches, once.
	noon := time.Date(2026, 10, 28, 12, 0, 0, 0, istanbul)
	n1, err := Tick(ctx, pool, noon)
	if err != nil || n1 == 0 {
		t.Fatalf("first tick: %d %v", n1, err)
	}
	if n2, err := Tick(ctx, pool, noon); err != nil || n2 != 0 {
		t.Fatalf("second tick at the same time inserted %d (%v)", n2, err)
	}
	// 28 Oct is a half holiday: people leave around 13:00, so the 14:00 tick
	// brings the out punches.
	if n3, _ := Tick(ctx, pool, noon.Add(2*time.Hour)); n3 == 0 {
		t.Fatal("no out punches by 14:00 on a half holiday")
	}
}
