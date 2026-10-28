package payroll

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/db"
	"bordroplus/internal/money"
	"bordroplus/internal/org"
	"bordroplus/internal/secret"
)

var (
	ErrNoRuleSet   = errors.New("no payroll rule set for period")
	ErrNotEmployed = errors.New("employee not employed in period")
	ErrNoSalary    = errors.New("no salary in force")
	// ErrPeriodFrozen: the period is under review, approved or closed; the
	// database trigger rejected the write.
	ErrPeriodFrozen = errors.New("payroll period is frozen")
)

// frozen maps the trigger's object_not_in_prerequisite_state to ErrPeriodFrozen.
func frozen(err error) error {
	if db.PgCode(err) == "55000" {
		return fmt.Errorf("%w: %v", ErrPeriodFrozen, err)
	}
	return err
}

// Service builds inputs from HR data and stores results.
type Service struct {
	pool *pgxpool.Pool
	box  *secret.Box
}

func NewService(pool *pgxpool.Pool, box *secret.Box) *Service { return &Service{pool: pool, box: box} }

// MonthStart normalises t to the first day of its month.
func MonthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// RuleSetFor returns the rule set in force on the first day of period.
func RuleSetFor(ctx context.Context, q db.Querier, period time.Time) (RuleSet, error) {
	var rs RuleSet
	var brackets []byte
	err := q.QueryRow(ctx, `
		SELECT version, lower(valid_during), upper(valid_during), min_wage_gross, sgk_floor, sgk_ceiling,
		       sgk_employee_rate, unemp_employee_rate, sgk_employer_rate, employer_discount, unemp_employer_rate,
		       stamp_tax_rate, tax_brackets
		  FROM payroll_rule_sets WHERE valid_during @> $1::date`, period).
		Scan(&rs.Version, &rs.ValidFrom, &rs.ValidTo, &rs.MinWageGross, &rs.SGKFloor, &rs.SGKCeiling,
			&rs.SGKEmployee, &rs.UnempEmployee, &rs.SGKEmployer, &rs.EmployerDiscount, &rs.UnempEmployer,
			&rs.StampTax, &brackets)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuleSet{}, fmt.Errorf("%w %s", ErrNoRuleSet, period.Format("2006-01"))
	}
	if err != nil {
		return RuleSet{}, err
	}
	// The table stores bracket limits as integer kuruş.
	var stored []struct {
		UpTo *int64 `json:"upto"`
		Rate Rate   `json:"rate"`
	}
	if err := json.Unmarshal(brackets, &stored); err != nil {
		return RuleSet{}, err
	}
	for _, b := range stored {
		br := Bracket{Rate: b.Rate}
		if b.UpTo != nil {
			v := Kurus(*b.UpTo)
			br.UpTo = &v
		}
		rs.Brackets = append(rs.Brackets, br)
	}
	return rs, nil
}

// rules caches rule sets for one calculation run, so 1.500 employees do
// not re-read the same rows. Safe for concurrent use.
type rules struct {
	mu   sync.Mutex
	sets map[time.Time]RuleSet
}

func newRules() *rules { return &rules{sets: map[time.Time]RuleSet{}} }

func (c *rules) get(ctx context.Context, q db.Querier, period time.Time) (RuleSet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rs, ok := c.sets[period]; ok {
		return rs, nil
	}
	rs, err := RuleSetFor(ctx, q, period)
	if err == nil {
		c.sets[period] = rs
	}
	return rs, err
}

// minWageCumBefore is the minimum wage's cumulative tax base for the months
// of the year before period, each under its own rule set. It is the same for
// every employee: the exemption follows the minimum wage, not the person.
func (c *rules) minWageCumBefore(ctx context.Context, q db.Querier, period time.Time) (Kurus, error) {
	var cum Kurus
	for m := time.Date(period.Year(), 1, 1, 0, 0, 0, 0, time.UTC); m.Before(period); m = m.AddDate(0, 1, 0) {
		rs, err := c.get(ctx, q, m)
		if err != nil {
			return 0, err
		}
		cum += rs.MinWageGross - pct(rs.MinWageGross, rs.SGKEmployee) - pct(rs.MinWageGross, rs.UnempEmployee)
	}
	return cum, nil
}

// Context records where the inputs came from, stored next to them.
type Context struct {
	TimesheetStatus string   `json:"timesheet_status"` // approved | not_approved
	UnpaidDays      int      `json:"unpaid_leave_days"`
	SalaryChanges   []string `json:"salary_changes_in_month"`
	Notes           []string `json:"notes"`
}

// BuildInput gathers one employee-month from HR data. Base pay follows the
// 30-day convention: a full month pays the monthly salary (days weighted
// across a mid-month raise), a partial month pays salary ÷ 30 per employed
// day, and approved unpaid leave removes salary ÷ 30 per day.
func (s *Service) BuildInput(ctx context.Context, q db.Querier, employeeID int64, period time.Time, prevCum Kurus) (Input, Context, error) {
	return s.buildInput(ctx, q, newRules(), employeeID, period, prevCum)
}

func (s *Service) buildInput(ctx context.Context, q db.Querier, c *rules, employeeID int64, period time.Time, prevCum Kurus) (Input, Context, error) {
	start := MonthStart(period)
	end := start.AddDate(0, 1, -1)
	var hire time.Time
	var term *time.Time
	if err := q.QueryRow(ctx, `SELECT hire_date, termination_date FROM employees WHERE id = $1`, employeeID).Scan(&hire, &term); err != nil {
		return Input{}, Context{}, err
	}
	from, to := start, end
	if hire.After(from) {
		from = hire
	}
	if term != nil && term.Before(to) {
		to = *term
	}
	if to.Before(from) {
		return Input{}, Context{}, ErrNotEmployed
	}

	salaries, err := s.salaries(ctx, q, employeeID, to)
	if err != nil {
		return Input{}, Context{}, err
	}
	salaryOn := func(d time.Time) (Kurus, bool) {
		var v Kurus
		ok := false
		for _, sr := range salaries {
			if sr.from.After(d) {
				break
			}
			v, ok = sr.amount, true
		}
		return v, ok
	}

	var ic Context
	for _, sr := range salaries {
		if !sr.from.Before(start) && !sr.from.After(end) {
			ic.SalaryChanges = append(ic.SalaryChanges, sr.from.Format("2006-01-02")+": "+sr.amount.String())
		}
	}

	unpaid, err := unpaidDays(ctx, q, employeeID, from, to)
	if err != nil {
		return Input{}, Context{}, err
	}
	full := from.Equal(start) && to.Equal(end)
	monthDays := int64(end.Day())
	var sumAll, sumUnpaid int64
	employed := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		v, ok := salaryOn(d)
		if !ok {
			return Input{}, Context{}, fmt.Errorf("%w on %s", ErrNoSalary, d.Format("2006-01-02"))
		}
		sumAll += int64(v)
		employed++
		if unpaid[d] {
			sumUnpaid += int64(v)
			ic.UnpaidDays++
		}
	}
	in := Input{Period: start, OvertimePct: 150, RestDayPct: 150, HolidayPct: 200, PrevCumTaxBase: prevCum}
	if full {
		in.PaidDays = 30
		in.BasePay = Kurus(roundDiv(sumAll*30-sumUnpaid*monthDays, 30*monthDays))
	} else {
		in.PaidDays = min(employed, 30)
		in.BasePay = Kurus(roundDiv(sumAll-sumUnpaid, 30))
		ic.Notes = append(ic.Notes, fmt.Sprintf("kısmi ay: %s - %s", from.Format("2006-01-02"), to.Format("2006-01-02")))
	}
	in.PaidDays = max(0, in.PaidDays-ic.UnpaidDays)
	in.HourlyBase, _ = salaryOn(to)

	// Overtime comes only from an approved (frozen) timesheet month.
	var snapshot []byte
	err = q.QueryRow(ctx, `SELECT totals FROM timesheet_periods WHERE employee_id = $1 AND month = $2`, employeeID, start).Scan(&snapshot)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		ic.TimesheetStatus = "not_approved"
		ic.Notes = append(ic.Notes, "puantaj onaylanmamış: fazla mesai bu bordroya girmedi")
	case err != nil:
		return Input{}, Context{}, err
	default:
		var ts struct {
			Totals struct {
				Overtime int `json:"overtime_minutes"`
				RestDay  int `json:"rest_day_minutes"`
				Holiday  int `json:"holiday_minutes"`
			} `json:"totals"`
			Multipliers struct {
				Overtime int `json:"overtime"`
				RestDay  int `json:"rest_day"`
				Holiday  int `json:"holiday"`
			} `json:"multipliers_pct"`
		}
		if err := json.Unmarshal(snapshot, &ts); err != nil {
			return Input{}, Context{}, err
		}
		ic.TimesheetStatus = "approved"
		in.OvertimeMinutes, in.RestDayMinutes, in.HolidayMinutes = ts.Totals.Overtime, ts.Totals.RestDay, ts.Totals.Holiday
		in.OvertimePct, in.RestDayPct, in.HolidayPct = ts.Multipliers.Overtime, ts.Multipliers.RestDay, ts.Multipliers.Holiday
	}
	in.Items = []Item{} // advances, loans and bonuses: payroll items module

	in.PrevCumMinWageTaxBase, err = c.minWageCumBefore(ctx, q, start)
	return in, ic, err
}

type salaryRow struct {
	from   time.Time
	amount Kurus
}

func (s *Service) salaries(ctx context.Context, q db.Querier, employeeID int64, upTo time.Time) ([]salaryRow, error) {
	rows, err := q.Query(ctx, `
		SELECT valid_from, amount_enc FROM employee_salaries WHERE employee_id = $1 AND valid_from <= $2 ORDER BY valid_from`,
		employeeID, upTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []salaryRow
	for rows.Next() {
		var from time.Time
		var enc []byte
		if err := rows.Scan(&from, &enc); err != nil {
			return nil, err
		}
		v, err := s.box.OpenInt64(enc, org.SalaryAAD(employeeID, from))
		if err != nil {
			return nil, fmt.Errorf("salary of employee %d from %s: %w", employeeID, from.Format("2006-01-02"), err)
		}
		out = append(out, salaryRow{from: from, amount: money.Kurus(v)})
	}
	return out, rows.Err()
}

// unpaidDays are the calendar days in [from, to] covered by approved unpaid leave.
func unpaidDays(ctx context.Context, q db.Querier, employeeID int64, from, to time.Time) (map[time.Time]bool, error) {
	rows, err := q.Query(ctx, `
		SELECT d::date FROM leave_requests r, generate_series(greatest(r.start_date, $2::date), least(r.end_date, $3::date), interval '1 day') d
		 WHERE r.employee_id = $1 AND r.leave_type = 'unpaid' AND r.status = 'approved'
		   AND r.start_date <= $3 AND r.end_date >= $2`, employeeID, from, to)
	if err != nil {
		return nil, err
	}
	days, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	out := map[time.Time]bool{}
	for _, d := range days {
		out[d] = true
	}
	return out, err
}

// Summary is the stored headline of a month.
type Summary struct {
	Period       string `json:"period"`
	RuleVersion  string `json:"rule_version"`
	Gross        string `json:"gross"`
	TaxBase      string `json:"tax_base"`
	CumTaxBase   string `json:"cum_tax_base"`
	IncomeTax    string `json:"income_tax"`
	Net          string `json:"net"`
	EmployerCost string `json:"employer_cost"`
}

func summary(period time.Time, r Result) Summary {
	return Summary{Period: period.Format("2006-01"), RuleVersion: r.RuleVersion, Gross: r.Gross.String(),
		TaxBase: r.TaxBase.String(), CumTaxBase: r.CumTaxBase.String(), IncomeTax: r.IncomeTax.String(),
		Net: r.Net.String(), EmployerCost: r.EmployerCost.String()}
}

// storedInputs is what payroll_results.inputs holds.
type storedInputs struct {
	Input   Input   `json:"input"`
	Context Context `json:"context"`
}

// prevCumTaxBase is the cumulative tax base before period: the stored
// result of the previous month of the same year, computed first if missing.
// Known limit: tax bases earned at a previous employer in the same year are
// not known to the system and are not carried in.
func (s *Service) prevCumTaxBase(ctx context.Context, tx pgx.Tx, c *rules, employeeID int64, period time.Time) (Kurus, error) {
	if period.Month() == time.January {
		return 0, nil
	}
	prev := period.AddDate(0, -1, 0)
	var cum Kurus
	err := tx.QueryRow(ctx, `SELECT cum_tax_base FROM payroll_results WHERE employee_id = $1 AND period = $2`, employeeID, prev).Scan(&cum)
	if err == nil {
		return cum, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	r, _, err := s.calculate(ctx, tx, c, employeeID, prev)
	// A month before the first salary record was not run through this system
	// (e.g. before go-live): the cumulative chain starts after it. Only the
	// month actually requested fails on a missing salary.
	if errors.Is(err, ErrNotEmployed) || errors.Is(err, ErrNoSalary) {
		return s.prevCumTaxBase(ctx, tx, c, employeeID, prev)
	}
	return r.CumTaxBase, err
}

// retroPrefix marks prior-period correction items.
const retroPrefix = "retro_"

// ownGross is a stored month's gross without corrections it carried for
// even earlier months.
func ownGross(r Result) Kurus {
	g := r.Gross
	for _, l := range r.Lines {
		if strings.HasPrefix(l.Code, retroPrefix) {
			g -= l.Amount
		}
	}
	return g
}

// retroItems finds closed months of the year whose gross would differ with
// today's data (a raise entered after closing, unpaid leave approved late,
// a timesheet approved late) and turns each difference into an item of this
// period. Closed months are never recalculated in place. Only months where
// relevant data changed after the close are re-derived; differences already
// paid in other periods are subtracted, so nothing is paid twice.
func (s *Service) retroItems(ctx context.Context, tx pgx.Tx, c *rules, employeeID int64, period time.Time) ([]Item, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM payroll_adjustments WHERE employee_id = $1 AND target_period = $2`, employeeID, period); err != nil {
		return nil, frozen(err)
	}
	rows, err := tx.Query(ctx, `
		WITH closed AS (
			SELECT period, closed_at FROM payroll_periods
			 WHERE status = 'closed' AND period >= date_trunc('year', $2::date) AND period < $2)
		SELECT DISTINCT c.period FROM closed c
		 WHERE EXISTS (SELECT 1 FROM employee_salaries s
		                WHERE s.employee_id = $1 AND s.created_at > c.closed_at AND s.valid_from < c.period + interval '1 month')
		    OR EXISTS (SELECT 1 FROM leave_requests r
		                WHERE r.employee_id = $1 AND r.leave_type = 'unpaid' AND r.status = 'approved' AND r.decided_at > c.closed_at
		                  AND r.start_date < c.period + interval '1 month' AND r.end_date >= c.period)
		    OR EXISTS (SELECT 1 FROM timesheet_periods t
		                WHERE t.employee_id = $1 AND t.month = c.period AND t.approved_at > c.closed_at)
		 ORDER BY 1`, employeeID, period)
	if err != nil {
		return nil, err
	}
	months, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, k := range months {
		var raw []byte
		var paidElsewhere Kurus
		err := tx.QueryRow(ctx, `
			SELECT r.result, COALESCE((SELECT sum(a.amount) FROM payroll_adjustments a
			                            WHERE a.employee_id = r.employee_id AND a.source_period = r.period
			                              AND a.target_period <> $3), 0)
			  FROM payroll_results r WHERE r.employee_id = $1 AND r.period = $2`, employeeID, k, period).Scan(&raw, &paidElsewhere)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // the employee had no payroll that month
		}
		if err != nil {
			return nil, err
		}
		var stored Result
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, err
		}
		rs, err := c.get(ctx, tx, k)
		if err != nil {
			return nil, err
		}
		in, _, err := s.buildInput(ctx, tx, c, employeeID, k, 0)
		if err != nil {
			return nil, err
		}
		now := Calculate(rs, in)
		delta := ownGross(now) - (ownGross(stored) + paidElsewhere)
		if delta == 0 {
			continue
		}
		label := "Geçmiş dönem düzeltmesi (" + k.Format("2006-01") + ")"
		if _, err := tx.Exec(ctx, `
			INSERT INTO payroll_adjustments (employee_id, source_period, target_period, amount, reason)
			VALUES ($1, $2, $3, $4, $5)`, employeeID, k, period, delta,
			fmt.Sprintf("kapalı dönem brütü %s, güncel veriyle %s", (ownGross(stored)+paidElsewhere).String(), ownGross(now).String())); err != nil {
			return nil, frozen(err)
		}
		items = append(items, Item{Code: retroPrefix + k.Format("2006-01"), Label: label, Amount: delta, Earning: true, SGK: true, Tax: true})
	}
	return items, nil
}

// calculate computes and stores one month; changed reports whether the
// stored result differs from before.
func (s *Service) calculate(ctx context.Context, tx pgx.Tx, c *rules, employeeID int64, period time.Time) (Result, bool, error) {
	period = MonthStart(period)
	rs, err := c.get(ctx, tx, period)
	if err != nil {
		return Result{}, false, err
	}
	// Inputs first: a month off payroll fails here, before the cumulative
	// chain is walked, which keeps the walk linear.
	in, ic, err := s.buildInput(ctx, tx, c, employeeID, period, 0)
	if err != nil {
		return Result{}, false, err
	}
	if in.PrevCumTaxBase, err = s.prevCumTaxBase(ctx, tx, c, employeeID, period); err != nil {
		return Result{}, false, err
	}
	retro, err := s.retroItems(ctx, tx, c, employeeID, period)
	if err != nil {
		return Result{}, false, err
	}
	in.Items = append(in.Items, retro...)
	r := Calculate(rs, in)
	inJSON, err := json.Marshal(storedInputs{Input: in, Context: ic})
	if err != nil {
		return Result{}, false, err
	}
	resJSON, err := json.Marshal(r)
	if err != nil {
		return Result{}, false, err
	}
	hash := InputHash(rs.Version, in)
	tag, err := tx.Exec(ctx, `
		INSERT INTO payroll_results (employee_id, period, rule_version, input_hash, inputs, result,
		                             gross, tax_base, cum_tax_base, income_tax, net, employer_cost)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (employee_id, period) DO UPDATE SET
		       rule_version = EXCLUDED.rule_version, input_hash = EXCLUDED.input_hash, inputs = EXCLUDED.inputs,
		       result = EXCLUDED.result, gross = EXCLUDED.gross, tax_base = EXCLUDED.tax_base,
		       cum_tax_base = EXCLUDED.cum_tax_base, income_tax = EXCLUDED.income_tax, net = EXCLUDED.net,
		       employer_cost = EXCLUDED.employer_cost, calculated_at = now()
		 WHERE payroll_results.input_hash <> EXCLUDED.input_hash OR payroll_results.rule_version <> EXCLUDED.rule_version`,
		employeeID, period, rs.Version, hash, inJSON, resJSON,
		r.Gross, r.TaxBase, r.CumTaxBase, r.IncomeTax, r.Net, r.EmployerCost)
	if err != nil {
		return Result{}, false, frozen(err)
	}
	return r, tag.RowsAffected() > 0, nil
}

// InputHash fingerprints a calculation: same rule version and input, same hash.
func InputHash(version string, in Input) []byte {
	b, _ := json.Marshal(in)
	h := sha256.New()
	h.Write([]byte(version + "\n"))
	h.Write(b)
	return h.Sum(nil)
}

// CalculateMonth computes and stores one month (and any missing earlier
// month of the year it depends on).
func (s *Service) CalculateMonth(ctx context.Context, employeeID int64, period time.Time) (Result, error) {
	var r Result
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockEmployee(ctx, tx, employeeID); err != nil {
			return err
		}
		var err error
		r, _, err = s.calculate(ctx, tx, newRules(), employeeID, period)
		return err
	})
	return r, err
}

// Change is one month's effect of a recalculation.
type Change struct {
	Period  string   `json:"period"`
	Before  *Summary `json:"before"`
	After   Summary  `json:"after"`
	Changed bool     `json:"changed"`
}

// RecalculateFrom recomputes from (inclusive) through the last stored month
// of that year, in order, so a correction to an earlier month (a retroactive
// raise, an approved timesheet) flows into every later month's cumulative
// tax base. Months whose inputs did not change are left untouched.
func (s *Service) RecalculateFrom(ctx context.Context, employeeID int64, from time.Time) ([]Change, error) {
	from = MonthStart(from)
	var changes []Change
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockEmployee(ctx, tx, employeeID); err != nil {
			return err
		}
		var last time.Time
		err := tx.QueryRow(ctx, `
			SELECT COALESCE(max(period), $2) FROM payroll_results
			 WHERE employee_id = $1 AND period >= $2 AND period < $3`,
			employeeID, from, time.Date(from.Year()+1, 1, 1, 0, 0, 0, 0, time.UTC)).Scan(&last)
		if err != nil {
			return err
		}
		c := newRules()
		for m := from; !m.After(last); m = m.AddDate(0, 1, 0) {
			var before *Summary
			var raw []byte
			err := tx.QueryRow(ctx, `SELECT result FROM payroll_results WHERE employee_id = $1 AND period = $2`, employeeID, m).Scan(&raw)
			if err == nil {
				var old Result
				if err := json.Unmarshal(raw, &old); err != nil {
					return err
				}
				sm := summary(m, old)
				before = &sm
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			r, changed, err := s.calculate(ctx, tx, c, employeeID, m)
			if errors.Is(err, ErrNotEmployed) {
				continue
			}
			if err != nil {
				return err
			}
			changes = append(changes, Change{Period: m.Format("2006-01"), Before: before, After: summary(m, r), Changed: changed})
		}
		return nil
	})
	return changes, err
}

// lockEmployee serialises payroll writes for one employee.
func lockEmployee(ctx context.Context, tx pgx.Tx, employeeID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7201, $1::int)`, employeeID)
	return err
}

// StoredResult loads a stored month with its inputs.
func (s *Service) StoredResult(ctx context.Context, employeeID int64, period time.Time) (Result, storedInputs, error) {
	var res, inp []byte
	err := s.pool.QueryRow(ctx, `SELECT result, inputs FROM payroll_results WHERE employee_id = $1 AND period = $2`,
		employeeID, MonthStart(period)).Scan(&res, &inp)
	if err != nil {
		return Result{}, storedInputs{}, err
	}
	var r Result
	var in storedInputs
	if err := json.Unmarshal(res, &r); err != nil {
		return Result{}, storedInputs{}, err
	}
	return r, in, json.Unmarshal(inp, &in)
}
