package payroll

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/audit"
	"bordroplus/internal/db"
)

// Period workflow: draft → review → approved → closed. A review can be
// rejected back to draft. Results are writable only in draft; the database
// enforces it (payroll_frozen_guard).
const (
	StatusDraft    = "draft"
	StatusReview   = "review"
	StatusApproved = "approved"
	StatusClosed   = "closed"
)

var (
	ErrInvalidTransition = errors.New("invalid period transition")
	ErrPreviousOpen      = errors.New("previous period is not closed")
	ErrNotRun            = errors.New("period has not been calculated without errors")
)

// Period is a payroll month as returned by the API.
type Period struct {
	Period        string         `json:"period"`
	Status        string         `json:"status"`
	RuleVersion   *string        `json:"rule_version"`
	EmployeeCount int            `json:"employee_count"`
	Totals        map[string]any `json:"totals"`
	Digest        string         `json:"digest"`
	LastRunAt     *time.Time     `json:"last_run_at"`
	LastRunMS     *int           `json:"last_run_ms"`
	Failures      int            `json:"last_run_failures"`
	SubmittedBy   *int64         `json:"submitted_by"`
	SubmittedAt   *time.Time     `json:"submitted_at"`
	ApprovedBy    *int64         `json:"approved_by"`
	ApprovedAt    *time.Time     `json:"approved_at"`
	ClosedBy      *int64         `json:"closed_by"`
	ClosedAt      *time.Time     `json:"closed_at"`
	ReviewNote    string         `json:"review_note"`
}

const periodSelect = `
	SELECT to_char(period, 'YYYY-MM'), status, rule_version, employee_count, totals, encode(digest, 'hex'),
	       last_run_at, last_run_ms, last_run_failures, submitted_by, submitted_at, approved_by, approved_at,
	       closed_by, closed_at, review_note
	  FROM payroll_periods`

func scanPeriod(row pgx.Row) (Period, error) {
	var p Period
	var totals []byte
	var digest *string
	err := row.Scan(&p.Period, &p.Status, &p.RuleVersion, &p.EmployeeCount, &totals, &digest, &p.LastRunAt, &p.LastRunMS,
		&p.Failures, &p.SubmittedBy, &p.SubmittedAt, &p.ApprovedBy, &p.ApprovedAt, &p.ClosedBy, &p.ClosedAt, &p.ReviewNote)
	if err != nil {
		return Period{}, err
	}
	if digest != nil {
		p.Digest = *digest
	}
	return p, json.Unmarshal(totals, &p.Totals)
}

// GetPeriod loads one period.
func (s *Service) GetPeriod(ctx context.Context, period time.Time) (Period, error) {
	return scanPeriod(s.pool.QueryRow(ctx, periodSelect+` WHERE period = $1`, MonthStart(period)))
}

// ListPeriods returns periods newest first.
func (s *Service) ListPeriods(ctx context.Context) ([]Period, error) {
	rows, err := s.pool.Query(ctx, periodSelect+` ORDER BY period DESC LIMIT 60`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Period, error) { return scanPeriod(row) })
}

// Actor identifies who performs a workflow step.
type Actor struct {
	UserID    int64
	Role      string
	RequestID string
	IP        string
}

func (a Actor) entry(action string, period time.Time, oldV, newV any) audit.Entry {
	return audit.Entry{ActorUserID: &a.UserID, ActorRole: a.Role, Action: action, EntityType: "payroll_period",
		EntityID: period.Format("2006-01"), Old: oldV, New: newV, RequestID: a.RequestID, IP: a.IP}
}

// Failure is an employee the run could not calculate.
type Failure struct {
	EmployeeID int64  `json:"employee_id"`
	Error      string `json:"error"`
}

// RunReport summarises a period run.
type RunReport struct {
	Period     string    `json:"period"`
	Employees  int       `json:"employees"`
	Changed    int       `json:"changed"`
	Unchanged  int       `json:"unchanged"`
	Failures   []Failure `json:"failures"`
	DurationMS int64     `json:"duration_ms"`
	Workers    int       `json:"workers"`
	Digest     string    `json:"digest"`
	Totals     Totals    `json:"totals"`
}

// Totals are period sums.
type Totals struct {
	Gross        Kurus `json:"gross"`
	Net          Kurus `json:"net"`
	IncomeTax    Kurus `json:"income_tax"`
	EmployerCost Kurus `json:"employer_cost"`
}

// RunPeriod calculates every employee on payroll in period, in parallel.
// The period row is locked for the whole run, so no submit or second run
// can interleave. Each employee is calculated in its own transaction by one
// of `workers` goroutines; employees are independent, and every calculation
// is a pure function of stored data, so the order does not matter and a
// re-run with the same rule set changes nothing (same digest).
func (s *Service) RunPeriod(ctx context.Context, actor Actor, period time.Time, workers int) (RunReport, error) {
	period = MonthStart(period)
	started := time.Now()
	rep := RunReport{Period: period.Format("2006-01"), Workers: workers, Failures: []Failure{}}

	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO payroll_periods (period) VALUES ($1) ON CONFLICT DO NOTHING`, period); err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM payroll_periods WHERE period = $1 FOR UPDATE`, period).Scan(&status); err != nil {
			return err
		}
		if status != StatusDraft {
			return fmt.Errorf("%w: period is %s", ErrPeriodFrozen, status)
		}
		if err := previousClosed(ctx, tx, period); err != nil {
			return err
		}
		rs, err := RuleSetFor(ctx, tx, period)
		if err != nil {
			return err
		}

		end := period.AddDate(0, 1, -1)
		rows, err := tx.Query(ctx, `
			SELECT e.id FROM employees e
			 WHERE e.hire_date <= $2 AND (e.termination_date IS NULL OR e.termination_date >= $1)
			   AND EXISTS (SELECT 1 FROM employee_salaries s WHERE s.employee_id = e.id AND s.valid_from <= $2)
			 ORDER BY e.id`, period, end)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		rep.Employees = len(ids)
		// Results of employees no longer on payroll this month (e.g. a
		// termination backdated) must not linger.
		if _, err := tx.Exec(ctx, `DELETE FROM payroll_results WHERE period = $1 AND employee_id <> ALL($2)`, period, ids); err != nil {
			return frozen(err)
		}

		cache := newRules()
		jobs := make(chan int64)
		var (
			wg sync.WaitGroup
			mu sync.Mutex
		)
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range jobs {
					var changed bool
					err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(etx pgx.Tx) error {
						if err := lockEmployee(ctx, etx, id); err != nil {
							return err
						}
						var err error
						_, changed, err = s.calculate(ctx, etx, cache, id, period)
						return err
					})
					mu.Lock()
					switch {
					case err != nil:
						rep.Failures = append(rep.Failures, Failure{EmployeeID: id, Error: err.Error()})
					case changed:
						rep.Changed++
					default:
						rep.Unchanged++
					}
					mu.Unlock()
				}
			}()
		}
		for _, id := range ids {
			select {
			case jobs <- id:
			case <-ctx.Done():
			}
		}
		close(jobs)
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return err
		}

		// Digest over every result's input hash in employee order: equal
		// digests mean an identical payroll.
		rows, err = tx.Query(ctx, `SELECT employee_id, input_hash FROM payroll_results WHERE period = $1 ORDER BY employee_id`, period)
		if err != nil {
			return err
		}
		h := sha256.New()
		for rows.Next() {
			var id int64
			var ih []byte
			if err := rows.Scan(&id, &ih); err != nil {
				rows.Close()
				return err
			}
			fmt.Fprintf(h, "%d:", id)
			h.Write(ih)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		digest := h.Sum(nil)
		rep.Digest = hex.EncodeToString(digest)
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(sum(gross), 0), COALESCE(sum(net), 0), COALESCE(sum(income_tax), 0), COALESCE(sum(employer_cost), 0)
			  FROM payroll_results WHERE period = $1`, period).
			Scan(&rep.Totals.Gross, &rep.Totals.Net, &rep.Totals.IncomeTax, &rep.Totals.EmployerCost); err != nil {
			return err
		}
		rep.DurationMS = time.Since(started).Milliseconds()
		totals, _ := json.Marshal(rep.Totals)
		if _, err := tx.Exec(ctx, `
			UPDATE payroll_periods SET rule_version = $2, employee_count = $3, totals = $4, digest = $5,
			       last_run_at = now(), last_run_ms = $6, last_run_failures = $7
			 WHERE period = $1`, period, rs.Version, rep.Employees, totals, digest, rep.DurationMS, len(rep.Failures)); err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor.entry("payroll.run", period, nil, map[string]any{
			"employees": rep.Employees, "changed": rep.Changed, "failures": len(rep.Failures),
			"duration_ms": rep.DurationMS, "digest": rep.Digest, "rule_version": rs.Version,
		}))
	})
	return rep, err
}

// previousClosed requires the previous month, if it was ever opened, to be
// closed: its cumulative tax bases feed this month.
func previousClosed(ctx context.Context, tx pgx.Tx, period time.Time) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM payroll_periods WHERE period = $1`, period.AddDate(0, -1, 0)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != StatusClosed {
		return fmt.Errorf("%w (%s is %s)", ErrPreviousOpen, period.AddDate(0, -1, 0).Format("2006-01"), status)
	}
	return nil
}

// transitions: action → from status, to status.
var transitions = map[string][2]string{
	"submit":  {StatusDraft, StatusReview},
	"approve": {StatusReview, StatusApproved},
	"reject":  {StatusReview, StatusDraft},
	"close":   {StatusApproved, StatusClosed},
}

// Transition moves a period through the workflow. Permission checks are the
// caller's; this enforces the state machine, the preconditions and the
// rule that whoever submitted cannot approve.
func (s *Service) Transition(ctx context.Context, actor Actor, period time.Time, action, note string) (Period, error) {
	period = MonthStart(period)
	t, ok := transitions[action]
	if !ok {
		return Period{}, ErrInvalidTransition
	}
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		p, err := scanPeriod(tx.QueryRow(ctx, periodSelect+` WHERE period = $1 FOR UPDATE`, period))
		if err != nil {
			return err
		}
		if p.Status != t[0] {
			return fmt.Errorf("%w: %s needs %s, period is %s", ErrInvalidTransition, action, t[0], p.Status)
		}
		// $1 period, $2 new status, $3 the acting user or the note.
		var set string
		var third any = actor.UserID
		switch action {
		case "submit":
			if p.LastRunAt == nil || p.Failures > 0 || p.EmployeeCount == 0 {
				return ErrNotRun
			}
			if err := previousClosed(ctx, tx, period); err != nil {
				return err
			}
			set = `submitted_by = $3, submitted_at = now(), review_note = ''`
		case "approve":
			if p.SubmittedBy != nil && *p.SubmittedBy == actor.UserID {
				return fmt.Errorf("%w: the submitter cannot approve", ErrInvalidTransition)
			}
			set = `approved_by = $3, approved_at = now()`
		case "reject":
			set, third = `review_note = $3, submitted_by = NULL, submitted_at = NULL`, note
		case "close":
			set = `closed_by = $3, closed_at = now()`
		}
		if _, err := tx.Exec(ctx, `UPDATE payroll_periods SET status = $2, `+set+` WHERE period = $1`, period, t[1], third); err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor.entry("payroll."+action, period,
			map[string]string{"status": p.Status}, map[string]string{"status": t[1], "note": note}))
	})
	if err != nil {
		return Period{}, err
	}
	return s.GetPeriod(ctx, period)
}
