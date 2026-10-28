package payroll

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInstalmentsAddUp(t *testing.T) {
	for _, c := range []struct {
		total Kurus
		n     int
		want  []Kurus
	}{
		{1000000, 3, []Kurus{333333, 333333, 333334}},
		{100, 3, []Kurus{33, 33, 34}},
		{500000, 1, []Kurus{500000}},
		{7, 7, []Kurus{1, 1, 1, 1, 1, 1, 1}},
	} {
		got := Instalments(c.total, c.n)
		var sum Kurus
		for i := range got {
			sum += got[i]
			if got[i] != c.want[i] {
				t.Errorf("Instalments(%s, %d) = %v, want %v", c.total, c.n, got, c.want)
				break
			}
		}
		if sum != c.total {
			t.Errorf("instalments of %s sum to %s", c.total, sum)
		}
	}
}

func TestItemsFlowIntoPayroll(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	payroll := f.actor("bordro", "payroll")
	create := func(kind string, amount Kurus, n, m int) (PayrollItem, error) {
		return f.svc.CreateItem(ctx, payroll, NewItem{EmployeeID: f.emp, Kind: kind, Label: kind, Amount: amount, Instalments: n, Start: month(m)})
	}

	base := f.calc(2)
	loan, err := create(KindLoan, 1000000, 3, 2) // 10.000 TL over Feb-Apr
	if err != nil || len(loan.Schedule) != 3 || loan.Schedule[2].Period != "2026-04" || loan.Schedule[2].Amount != 333334 {
		t.Fatalf("loan: %+v %v", loan, err)
	}
	if _, err := create(KindBonus, 500000, 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := create(KindAdvance, 200000, 1, 3); err != nil {
		t.Fatal(err)
	}

	feb := f.calc(2)
	// The bonus is taxed like salary: compare with a 70.000 TL month.
	ref := Calculate(rules2026, full(2, 7000000, base.CumTaxBase-base.TaxBase, base.CumMinWageTaxBase-base.MinWageTaxBase))
	if feb.Gross != 7000000 || feb.IncomeTax != ref.IncomeTax || feb.NetDeductions != 333333 || feb.Net != ref.Net-333333 {
		t.Fatalf("February: gross %s tax %s ded %s net %s (ref net %s)", feb.Gross, feb.IncomeTax, feb.NetDeductions, feb.Net, ref.Net)
	}
	mar := f.calc(3)
	if mar.NetDeductions != 333333+200000 || mar.ExtraPay != 0 {
		t.Fatalf("March deductions %s extra %s", mar.NetDeductions, mar.ExtraPay)
	}
	apr := f.calc(4)
	may := f.calc(5)
	if apr.NetDeductions != 333334 || may.NetDeductions != 0 {
		t.Fatalf("April %s, May %s", apr.NetDeductions, may.NetDeductions)
	}

	// Deduction cap: a quarter of 65.000 is 16.250.
	if _, err := create(KindAdvance, 1625001, 1, 6); !errors.Is(err, ErrDeductionCap) {
		t.Fatalf("advance over the cap: %v", err)
	}
	if _, err := create(KindLoan, 4875000, 3, 6); err != nil {
		t.Fatalf("loan at the cap: %v", err)
	}

	// Cancelling withdraws future instalments.
	if _, err := f.svc.CancelItem(ctx, payroll, loan.ID); err != nil {
		t.Fatal(err)
	}
	if again := f.calc(4); again.NetDeductions != 0 {
		t.Fatalf("cancelled loan still deducted: %s", again.NetDeductions)
	}
	if _, err := f.svc.CancelItem(ctx, payroll, loan.ID); !errors.Is(err, ErrBadItem) {
		t.Fatalf("second cancel: %v", err)
	}
}

func TestItemsRespectFrozenPeriods(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	payroll := f.actor("bordro", "payroll")
	f.run(1)
	if _, err := f.svc.Transition(ctx, payroll, month(1), "submit", ""); err != nil {
		t.Fatal(err)
	}
	// January is under review: nothing may land in it.
	_, err := f.svc.CreateItem(ctx, payroll, NewItem{EmployeeID: f.emp, Kind: KindBonus, Label: "prim", Amount: 100000, Instalments: 1, Start: month(1)})
	if !errors.Is(err, ErrPeriodFrozen) {
		t.Fatalf("item into a period under review: %v", err)
	}
	// A loan starting in December 2025 would reach January too.
	_, err = f.svc.CreateItem(ctx, payroll, NewItem{EmployeeID: f.emp, Kind: KindLoan, Label: "borç", Amount: 100000, Instalments: 2,
		Start: month(1).AddDate(0, -1, 0)})
	if !errors.Is(err, ErrPeriodFrozen) {
		t.Fatalf("loan reaching a frozen period: %v", err)
	}
}

func TestPayslipVisibilityAndHTML(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.run(1)

	if _, err := f.svc.GetPayslip(ctx, f.emp, month(1), true); !errors.Is(err, ErrPayslipNotFinal) {
		t.Fatalf("employee view of a draft: %v", err)
	}
	draft, err := f.svc.GetPayslip(ctx, f.emp, month(1), false)
	if err != nil || draft.Final || draft.PeriodStatus != StatusDraft {
		t.Fatalf("payroll view of a draft: %+v %v", draft, err)
	}
	f.closeMonth(1)
	slip, err := f.svc.GetPayslip(ctx, f.emp, month(1), true)
	if err != nil || !slip.Final || slip.Net != 5093118 || slip.Employee.EmployeeNo != "D0001" || slip.Deductions != 6500000-5093118 {
		t.Fatalf("final payslip: %+v %v", slip, err)
	}

	var buf bytes.Buffer
	slip.Employee.Name = `<script>alert(1)</script>`
	if err := slip.WriteHTML(&buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, "50931.18") || !strings.Contains(html, "Gelir vergisi") || strings.Contains(html, "<script>alert") {
		t.Fatalf("payslip HTML wrong or not escaped:\n%s", html)
	}
}
