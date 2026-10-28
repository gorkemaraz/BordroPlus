// Package payroll computes Turkish monthly payroll. This file is the pure
// engine: gross to net with SGK and unemployment premiums, cumulative
// income tax over the annual wage tariff, stamp tax, and the minimum-wage
// income and stamp tax exemptions.
//
// Amounts are integer kuruş (money.Kurus); rates are integers in units of
// 1/100000. Rounding rule, applied everywhere: every line item is rounded
// half-up to the kuruş once, from exact integer arithmetic; totals are sums
// of rounded items. Income tax for a month is T(cum + base) - T(cum) on exact
// values, rounded once, so the monthly taxes of a year add up to the tax
// on the yearly total (to within a kuruş per month).
package payroll

import (
	"fmt"
	"time"

	"bordroplus/internal/money"
)

type Kurus = money.Kurus

// Rate is a percentage in units of 1/100000: 14% = 14000.
type Rate int64

const rateUnit = 100000

// Bracket is one band of the annual tariff; UpTo nil is the top band.
type Bracket struct {
	UpTo *Kurus `json:"upto"`
	Rate Rate   `json:"rate"`
}

// RuleSet is one row of payroll_rule_sets.
type RuleSet struct {
	Version          string    `json:"version"`
	ValidFrom        time.Time `json:"valid_from"`
	ValidTo          time.Time `json:"valid_to"` // exclusive
	MinWageGross     Kurus     `json:"min_wage_gross"`
	SGKFloor         Kurus     `json:"sgk_floor"`
	SGKCeiling       Kurus     `json:"sgk_ceiling"`
	SGKEmployee      Rate      `json:"sgk_employee_rate"`
	UnempEmployee    Rate      `json:"unemp_employee_rate"`
	SGKEmployer      Rate      `json:"sgk_employer_rate"`
	EmployerDiscount Rate      `json:"employer_discount"`
	UnempEmployer    Rate      `json:"unemp_employer_rate"`
	StampTax         Rate      `json:"stamp_tax_rate"`
	Brackets         []Bracket `json:"tax_brackets"`
}

// roundDiv divides n by d (d > 0) rounding half away from zero.
func roundDiv(n, d int64) int64 {
	if n >= 0 {
		return (n + d/2) / d
	}
	return -((-n + d/2) / d)
}

// pct applies a rate to an amount, rounded half-up to the kuruş.
func pct(k Kurus, r Rate) Kurus { return Kurus(roundDiv(int64(k)*int64(r), rateUnit)) }

// taxExact is the tariff tax on an annual cumulative base, in units of
// kuruş/rateUnit (unrounded).
func taxExact(base Kurus, br []Bracket) int64 {
	var tax int64
	var lower Kurus
	for _, b := range br {
		if base <= lower {
			break
		}
		upper := base
		if b.UpTo != nil && *b.UpTo < base {
			upper = *b.UpTo
		}
		tax += int64(upper-lower) * int64(b.Rate)
		if b.UpTo == nil {
			break
		}
		lower = *b.UpTo
	}
	return tax
}

// MonthlyTax is the income tax of a month whose base is added to an annual
// cumulative base of prev.
func MonthlyTax(prev, base Kurus, br []Bracket) Kurus {
	if base <= 0 {
		return 0
	}
	return Kurus(roundDiv(taxExact(prev+base, br)-taxExact(prev, br), rateUnit))
}

// Item is an extra earning or deduction for the month.
type Item struct {
	Code   string `json:"code"`
	Label  string `json:"label"`
	Amount Kurus  `json:"amount"`
	// Earning items are gross additions; SGK and Tax say whether they are
	// subject to premiums and to income/stamp tax. Non-earning items are
	// deducted from net pay (advances, loan instalments).
	Earning bool `json:"earning"`
	SGK     bool `json:"sgk"`
	Tax     bool `json:"tax"`
}

// Input is everything a month's calculation depends on.
type Input struct {
	Period     time.Time `json:"period"`      // first day of the month
	PaidDays   int       `json:"paid_days"`   // SGK days, 30 for a full month
	BasePay    Kurus     `json:"base_pay"`    // prorated monthly salary
	HourlyBase Kurus     `json:"hourly_base"` // monthly salary the hourly rate derives from (÷ 225)

	OvertimeMinutes int `json:"overtime_minutes"`
	RestDayMinutes  int `json:"rest_day_minutes"`
	HolidayMinutes  int `json:"holiday_minutes"`
	OvertimePct     int `json:"overtime_pct"` // 150
	RestDayPct      int `json:"rest_day_pct"` // 150
	HolidayPct      int `json:"holiday_pct"`  // 200 in total; the salary already pays 100

	Items []Item `json:"items"`

	PrevCumTaxBase        Kurus `json:"prev_cum_tax_base"`          // this year's earlier months
	PrevCumMinWageTaxBase Kurus `json:"prev_cum_min_wage_tax_base"` // same, for the minimum wage
}

// Line is one explained row of a payslip.
type Line struct {
	Code    string `json:"code"`
	Label   string `json:"label"`
	Amount  Kurus  `json:"amount"`
	Formula string `json:"formula"`
}

// Result is a computed month.
type Result struct {
	RuleVersion string `json:"rule_version"`

	BasePay     Kurus `json:"base_pay"`
	OvertimePay Kurus `json:"overtime_pay"`
	RestDayPay  Kurus `json:"rest_day_pay"`
	HolidayPay  Kurus `json:"holiday_pay"`
	ExtraPay    Kurus `json:"extra_pay"`
	Gross       Kurus `json:"gross"`

	SGKBase       Kurus `json:"sgk_base"`
	SGKEmployee   Kurus `json:"sgk_employee"`
	UnempEmployee Kurus `json:"unemp_employee"`

	TaxBase         Kurus `json:"tax_base"`
	CumTaxBase      Kurus `json:"cum_tax_base"`
	IncomeTaxCalc   Kurus `json:"income_tax_calculated"`
	IncomeTaxExempt Kurus `json:"income_tax_exemption"`
	IncomeTax       Kurus `json:"income_tax"`

	StampTaxCalc   Kurus `json:"stamp_tax_calculated"`
	StampTaxExempt Kurus `json:"stamp_tax_exemption"`
	StampTax       Kurus `json:"stamp_tax"`

	MinWageTaxBase    Kurus `json:"min_wage_tax_base"`
	CumMinWageTaxBase Kurus `json:"cum_min_wage_tax_base"`

	NetDeductions Kurus `json:"net_deductions"`
	Net           Kurus `json:"net"`

	SGKEmployer   Kurus `json:"sgk_employer"`
	UnempEmployer Kurus `json:"unemp_employer"`
	EmployerCost  Kurus `json:"employer_cost"`

	Lines []Line `json:"lines"`
}

func tl(k Kurus) string { return k.String() }

func pctStr(r Rate) string {
	return fmt.Sprintf("%%%s", trimZeros(fmt.Sprintf("%d.%03d", r/1000, r%1000)))
}

func trimZeros(s string) string {
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

// premiumPay is minutes × (monthly ÷ 225 ÷ 60) × pct ÷ 100, rounded once.
func premiumPay(monthly Kurus, minutes, pctOfHourly int) Kurus {
	if minutes <= 0 || pctOfHourly <= 0 {
		return 0
	}
	return Kurus(roundDiv(int64(monthly)*int64(minutes)*int64(pctOfHourly), 225*60*100))
}

// Calculate computes one month. It is deterministic: the same rule set and
// input always give the same result.
func Calculate(rs RuleSet, in Input) Result {
	r := Result{RuleVersion: rs.Version, BasePay: in.BasePay}
	add := func(code, label string, amount Kurus, formula string) {
		r.Lines = append(r.Lines, Line{Code: code, Label: label, Amount: amount, Formula: formula})
	}
	add("base_pay", "Temel ücret", r.BasePay, fmt.Sprintf("%d gün", in.PaidDays))

	// Extra time. The monthly salary already pays public holidays, so working
	// one adds HolidayPct - 100.
	r.OvertimePay = premiumPay(in.HourlyBase, in.OvertimeMinutes, in.OvertimePct)
	r.RestDayPay = premiumPay(in.HourlyBase, in.RestDayMinutes, in.RestDayPct)
	r.HolidayPay = premiumPay(in.HourlyBase, in.HolidayMinutes, in.HolidayPct-100)
	hourly := func(min, p int) string {
		return fmt.Sprintf("%s ÷ 225 × %d dk ÷ 60 × %%%d", tl(in.HourlyBase), min, p)
	}
	if r.OvertimePay > 0 {
		add("overtime_pay", "Fazla mesai", r.OvertimePay, hourly(in.OvertimeMinutes, in.OvertimePct))
	}
	if r.RestDayPay > 0 {
		add("rest_day_pay", "Hafta tatili çalışması", r.RestDayPay, hourly(in.RestDayMinutes, in.RestDayPct))
	}
	if r.HolidayPay > 0 {
		add("holiday_pay", "Resmî tatil çalışması", r.HolidayPay, hourly(in.HolidayMinutes, in.HolidayPct-100))
	}

	sgkable := r.BasePay + r.OvertimePay + r.RestDayPay + r.HolidayPay
	taxable := sgkable
	for _, it := range in.Items {
		if !it.Earning {
			r.NetDeductions += it.Amount
			continue
		}
		r.ExtraPay += it.Amount
		if it.SGK {
			sgkable += it.Amount
		}
		if it.Tax {
			taxable += it.Amount
		}
		add(it.Code, it.Label, it.Amount, "ek ödeme")
	}
	r.Gross = r.BasePay + r.OvertimePay + r.RestDayPay + r.HolidayPay + r.ExtraPay
	add("gross", "Brüt ücret", r.Gross, "")

	// Premium base, clamped to the floor and ceiling prorated by SGK days.
	floor := Kurus(roundDiv(int64(rs.SGKFloor)*int64(in.PaidDays), 30))
	ceiling := Kurus(roundDiv(int64(rs.SGKCeiling)*int64(in.PaidDays), 30))
	r.SGKBase = min(max(sgkable, floor), ceiling)
	if in.PaidDays == 0 {
		r.SGKBase = 0
	}
	add("sgk_base", "SGK matrahı", r.SGKBase, fmt.Sprintf("taban %s, tavan %s (%d gün)", tl(floor), tl(ceiling), in.PaidDays))
	r.SGKEmployee = pct(r.SGKBase, rs.SGKEmployee)
	r.UnempEmployee = pct(r.SGKBase, rs.UnempEmployee)
	add("sgk_employee", "SGK işçi payı", r.SGKEmployee, tl(r.SGKBase)+" × "+pctStr(rs.SGKEmployee))
	add("unemp_employee", "İşsizlik sigortası işçi payı", r.UnempEmployee, tl(r.SGKBase)+" × "+pctStr(rs.UnempEmployee))

	// Income tax on the cumulative annual base.
	r.TaxBase = max(0, taxable-r.SGKEmployee-r.UnempEmployee)
	r.CumTaxBase = in.PrevCumTaxBase + r.TaxBase
	r.IncomeTaxCalc = MonthlyTax(in.PrevCumTaxBase, r.TaxBase, rs.Brackets)
	add("tax_base", "Gelir vergisi matrahı", r.TaxBase, fmt.Sprintf("kümülatif %s → %s", tl(in.PrevCumTaxBase), tl(r.CumTaxBase)))
	add("income_tax_calculated", "Hesaplanan gelir vergisi", r.IncomeTaxCalc, "T(kümülatif) − T(önceki kümülatif)")

	// Minimum wage exemption: the tax the minimum wage itself would bear this
	// month, on its own cumulative base (GVK art. 23/18), prorated by days.
	mwGross := Kurus(roundDiv(int64(rs.MinWageGross)*int64(min(in.PaidDays, 30)), 30))
	r.MinWageTaxBase = mwGross - pct(mwGross, rs.SGKEmployee) - pct(mwGross, rs.UnempEmployee)
	r.CumMinWageTaxBase = in.PrevCumMinWageTaxBase + r.MinWageTaxBase
	r.IncomeTaxExempt = min(MonthlyTax(in.PrevCumMinWageTaxBase, r.MinWageTaxBase, rs.Brackets), r.IncomeTaxCalc)
	r.IncomeTax = r.IncomeTaxCalc - r.IncomeTaxExempt
	add("income_tax_exemption", "Asgari ücret gelir vergisi istisnası", -r.IncomeTaxExempt,
		fmt.Sprintf("asgari ücret matrahı %s, kümülatif %s", tl(r.MinWageTaxBase), tl(r.CumMinWageTaxBase)))
	add("income_tax", "Gelir vergisi", r.IncomeTax, "")

	// Stamp tax on taxable gross, minimum wage exempt (DVK table 2, 4-b).
	r.StampTaxCalc = pct(taxable, rs.StampTax)
	r.StampTaxExempt = min(pct(mwGross, rs.StampTax), r.StampTaxCalc)
	r.StampTax = r.StampTaxCalc - r.StampTaxExempt
	add("stamp_tax_calculated", "Hesaplanan damga vergisi", r.StampTaxCalc, tl(taxable)+" × "+pctStr(rs.StampTax))
	add("stamp_tax_exemption", "Asgari ücret damga vergisi istisnası", -r.StampTaxExempt, tl(mwGross)+" × "+pctStr(rs.StampTax))
	add("stamp_tax", "Damga vergisi", r.StampTax, "")

	for _, it := range in.Items {
		if !it.Earning {
			add(it.Code, it.Label, -it.Amount, "netten kesinti")
		}
	}
	r.Net = r.Gross - r.SGKEmployee - r.UnempEmployee - r.IncomeTax - r.StampTax - r.NetDeductions
	add("net", "Net ödenecek", r.Net, "")

	r.SGKEmployer = pct(r.SGKBase, rs.SGKEmployer-rs.EmployerDiscount)
	r.UnempEmployer = pct(r.SGKBase, rs.UnempEmployer)
	r.EmployerCost = r.Gross + r.SGKEmployer + r.UnempEmployer
	add("sgk_employer", "SGK işveren payı", r.SGKEmployer,
		fmt.Sprintf("%s × (%s − %s indirim)", tl(r.SGKBase), pctStr(rs.SGKEmployer), pctStr(rs.EmployerDiscount)))
	add("unemp_employer", "İşsizlik sigortası işveren payı", r.UnempEmployer, tl(r.SGKBase)+" × "+pctStr(rs.UnempEmployer))
	add("employer_cost", "İşveren maliyeti", r.EmployerCost, "")
	return r
}
