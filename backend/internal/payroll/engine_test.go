package payroll

import (
	"testing"
	"time"
)

func k(v int64) *Kurus { x := Kurus(v); return &x }

// rules2026 mirrors the TR-2026.1 row of the migration.
var rules2026 = RuleSet{
	Version: "TR-2026.1", MinWageGross: 3303000, SGKFloor: 3303000, SGKCeiling: 29727000,
	SGKEmployee: 14000, UnempEmployee: 1000, SGKEmployer: 21750, EmployerDiscount: 2000, UnempEmployer: 2000, StampTax: 759,
	Brackets: []Bracket{{k(19000000), 15000}, {k(40000000), 20000}, {k(150000000), 27000}, {k(530000000), 35000}, {nil, 40000}},
}

var rules2025 = RuleSet{
	Version: "TR-2025.1", MinWageGross: 2600550, SGKFloor: 2600550, SGKCeiling: 19504125,
	SGKEmployee: 14000, UnempEmployee: 1000, SGKEmployer: 20750, EmployerDiscount: 4000, UnempEmployer: 2000, StampTax: 759,
	Brackets: []Bracket{{k(15800000), 15000}, {k(33000000), 20000}, {k(120000000), 27000}, {k(430000000), 35000}, {nil, 40000}},
}

func month(m int) time.Time { return time.Date(2026, time.Month(m), 1, 0, 0, 0, 0, time.UTC) }

func full(m int, salary Kurus, prevCum, prevMW Kurus) Input {
	return Input{Period: month(m), PaidDays: 30, BasePay: salary, HourlyBase: salary,
		OvertimePct: 150, RestDayPct: 150, HolidayPct: 200, PrevCumTaxBase: prevCum, PrevCumMinWageTaxBase: prevMW}
}

// The published 2026 minimum wage: gross 33.030,00, net 28.075,50, employer
// cost 40.214,03 outside manufacturing. No income or stamp tax.
func TestMinimumWage2026(t *testing.T) {
	r := Calculate(rules2026, full(1, 3303000, 0, 0))
	want := map[string][2]Kurus{
		"sgk_employee":   {r.SGKEmployee, 462420},
		"unemp_employee": {r.UnempEmployee, 33030},
		"income_tax":     {r.IncomeTax, 0},
		"stamp_tax":      {r.StampTax, 0},
		"net":            {r.Net, 2807550},
		"sgk_employer":   {r.SGKEmployer, 652343},
		"unemp_employer": {r.UnempEmployer, 66060},
		"employer_cost":  {r.EmployerCost, 4021403},
		"exemption":      {r.IncomeTaxExempt, 421133}, // 28.075,50 × 15% = 4.211,325
	}
	for name, v := range want {
		if v[0] != v[1] {
			t.Errorf("%s = %s, want %s", name, v[0], v[1])
		}
	}
}

func TestMinimumWage2025(t *testing.T) {
	r := Calculate(rules2025, full(1, 2600550, 0, 0))
	// 26.005,50 - 3.640,77 - 260,06 = 22.104,67 (published net).
	if r.Net != 2210467 || r.IncomeTax != 0 || r.StampTax != 0 {
		t.Fatalf("2025 minimum wage: %+v", r)
	}
}

// A 65.000 TL salary in January 2026, by hand:
//
//	SGK 9.100,00; unemployment 650,00; tax base 55.250,00
//	tax 8.287,50 - exemption 4.211,33 = 4.076,17
//	stamp 493,35 - 250,70 = 242,65
//	net 65.000 - 9.100 - 650 - 4.076,17 - 242,65 = 50.931,18
func TestOrdinarySalaryJanuary(t *testing.T) {
	r := Calculate(rules2026, full(1, 6500000, 0, 0))
	if r.TaxBase != 5525000 || r.IncomeTaxCalc != 828750 || r.IncomeTax != 407617 ||
		r.StampTax != 24265 || r.Net != 5093118 {
		t.Fatalf("result: base=%s calc=%s tax=%s stamp=%s net=%s", r.TaxBase, r.IncomeTaxCalc, r.IncomeTax, r.StampTax, r.Net)
	}
	if len(r.Lines) == 0 || r.Lines[len(r.Lines)-1].Code != "employer_cost" {
		t.Fatal("payslip lines missing")
	}
}

// Cumulative tax: the base crosses the 190.000 bracket in April.
func TestCumulativeBracketCrossing(t *testing.T) {
	var cum, cumMW Kurus
	var taxes []Kurus
	for m := 1; m <= 12; m++ {
		r := Calculate(rules2026, full(m, 6500000, cum, cumMW))
		cum, cumMW = r.CumTaxBase, r.CumMinWageTaxBase
		taxes = append(taxes, r.IncomeTaxCalc)
	}
	// Jan-Mar 55.250 × 15% = 8.287,50. April: 24.250 × 15% + 31.000 × 20% = 9.837,50.
	if taxes[0] != 828750 || taxes[2] != 828750 || taxes[3] != 983750 || taxes[4] != 1105000 {
		t.Fatalf("monthly taxes: %v", taxes)
	}
	// The year's monthly taxes add up to the tariff tax of the yearly base.
	var sum Kurus
	for _, x := range taxes {
		sum += x
	}
	if want := MonthlyTax(0, cum, rules2026.Brackets); sum < want-12 || sum > want+12 {
		t.Fatalf("sum of months %s, yearly tax %s", sum, want)
	}
}

// The minimum wage's own cumulative base crosses 190.000 in July, so the
// exemption grows: (190.000 - 168.453) × 15% + 6.528,50 × 20% = 3.232,05 + 1.305,70 = 4.537,75;
// in August all of it is at 20%: 28.075,50 × 20% = 5.615,10.
func TestMinimumWageExemptionFollowsItsOwnCumulative(t *testing.T) {
	var cum, cumMW Kurus
	var ex []Kurus
	for m := 1; m <= 8; m++ {
		r := Calculate(rules2026, full(m, 15000000, cum, cumMW))
		cum, cumMW = r.CumTaxBase, r.CumMinWageTaxBase
		ex = append(ex, r.IncomeTaxExempt)
	}
	if ex[5] != 421133 || ex[6] != 453775 || ex[7] != 561510 {
		t.Fatalf("exemptions: %v", ex)
	}
}

func TestCeilingAndFloor(t *testing.T) {
	// 400.000 TL: premiums stop at the 297.270 TL ceiling.
	r := Calculate(rules2026, full(1, 40000000, 0, 0))
	if r.SGKBase != 29727000 || r.SGKEmployee != 4161780 {
		t.Fatalf("ceiling: base %s sgk %s", r.SGKBase, r.SGKEmployee)
	}
	// Half month: floor and ceiling are prorated by SGK days.
	in := full(1, 1651500, 0, 0)
	in.PaidDays = 15
	r = Calculate(rules2026, in)
	if r.SGKBase != 1651500 || r.Net != 1403775 {
		t.Fatalf("15 days of minimum wage: base %s net %s", r.SGKBase, r.Net)
	}
}

func TestOvertimeAndHolidayPay(t *testing.T) {
	in := full(1, 6750000, 0, 0) // 67.500 / 225 = 300 TL an hour
	in.OvertimeMinutes, in.RestDayMinutes, in.HolidayMinutes = 600, 120, 480
	r := Calculate(rules2026, in)
	// 10 h × 300 × 1.5 = 4.500; 2 h × 300 × 1.5 = 900; 8 h × 300 × 1.0 = 2.400.
	if r.OvertimePay != 450000 || r.RestDayPay != 90000 || r.HolidayPay != 240000 || r.Gross != 6750000+780000 {
		t.Fatalf("extra pay: %+v", r)
	}
}

func TestItems(t *testing.T) {
	in := full(1, 6500000, 0, 0)
	in.Items = []Item{
		{Code: "bonus", Label: "Prim", Amount: 1000000, Earning: true, SGK: true, Tax: true},
		{Code: "advance", Label: "Avans", Amount: 500000},
	}
	r := Calculate(rules2026, in)
	base := Calculate(rules2026, full(1, 7500000, 0, 0))
	if r.Gross != 7500000 || r.SGKBase != base.SGKBase || r.IncomeTax != base.IncomeTax || r.Net != base.Net-500000 {
		t.Fatalf("items: %+v", r)
	}
}

func TestDeterministic(t *testing.T) {
	in := full(5, 12345678, 50000000, 11230200)
	in.OvertimeMinutes = 777
	a, b := Calculate(rules2026, in), Calculate(rules2026, in)
	if a.Net != b.Net || a.EmployerCost != b.EmployerCost || len(a.Lines) != len(b.Lines) {
		t.Fatal("same input, different result")
	}
}

func TestRoundDiv(t *testing.T) {
	cases := [][3]int64{{5, 10, 1}, {4, 10, 0}, {15, 10, 2}, {-5, 10, -1}, {-4, 10, 0}}
	for _, c := range cases {
		if got := roundDiv(c[0], c[1]); got != c[2] {
			t.Errorf("roundDiv(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

// Matches the published 2026 figures: 4.211,33 for January-June, 4.537,75
// from July (MUHSGK announcement), 250,70 stamp tax exemption, and the
// yearly total 57.881,23.
func TestMinimumWageExemptionYear2026(t *testing.T) {
	var cum, cumMW, total Kurus
	for m := 1; m <= 12; m++ {
		r := Calculate(rules2026, full(m, 9000000, cum, cumMW))
		cum, cumMW = r.CumTaxBase, r.CumMinWageTaxBase
		total += r.IncomeTaxExempt
		if r.StampTaxExempt != 25070 {
			t.Fatalf("month %d stamp exemption %s", m, r.StampTaxExempt)
		}
		if m == 7 && r.IncomeTaxExempt != 453775 {
			t.Fatalf("July exemption %s, published 4537.75", r.IncomeTaxExempt)
		}
	}
	if total != 5788123 {
		t.Fatalf("yearly exemption %s, published 57881.23", total)
	}
}
