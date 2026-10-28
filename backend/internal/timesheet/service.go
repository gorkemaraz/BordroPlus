package timesheet

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/audit"
	"bordroplus/internal/db"
	"bordroplus/internal/leave"
)

var (
	ErrPeriodLocked   = errors.New("timesheet period is approved")
	ErrPeriodOpen     = errors.New("period has not ended")
	ErrUnresolved     = errors.New("unresolved punch anomalies")
	ErrAlreadyDecided = errors.New("correction already decided or obsolete")
)

// lockSpace namespaces the per-employee advisory locks.
const lockSpace = 7101

// Service reads and writes timesheets.
type Service struct {
	pool  *pgxpool.Pool
	rules Rules
	now   func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, rules: DefaultRules, now: time.Now}
}

// Today is the current Istanbul date.
func (s *Service) Today() time.Time { return CivilDate(s.now()) }

// lockEmployee serialises timesheet writes of one employee (punch import,
// correction approval, month approval) for the rest of the transaction.
func lockEmployee(ctx context.Context, tx pgx.Tx, employeeID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2::int)`, lockSpace, employeeID)
	return err
}

// MonthOf is the payroll month a day belongs to: the month of its week's Sunday.
func MonthOf(d time.Time) time.Time {
	sunday := WeekStart(d).AddDate(0, 0, 6)
	return time.Date(sunday.Year(), sunday.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func localMidnight(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, Istanbul)
}

// effectivePunches are the raw punches minus approved drops; approved
// additions are already rows of their own.
func effectivePunches(ctx context.Context, q db.Querier, employeeID int64, from, to time.Time) ([]Punch, error) {
	rows, err := q.Query(ctx, `
		SELECT p.id, p.punched_at, p.direction FROM time_punches p
		 WHERE p.employee_id = $1 AND p.punched_at >= $2 AND p.punched_at < $3
		   AND NOT EXISTS (SELECT 1 FROM punch_corrections c
		                    WHERE c.target_punch_id = p.id AND c.kind = 'drop_duplicate' AND c.status = 'approved')
		 ORDER BY p.punched_at, p.id`, employeeID, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Punch, error) {
		var p Punch
		err := row.Scan(&p.ID, &p.At, &p.Dir)
		return p, err
	})
}

// Analysis is the computed state of an employee's days in [From, To].
type Analysis struct {
	From, To  time.Time
	Sessions  []Session // finalised, every session touching the window
	Anomalies []Anomaly // anomalies whose work date is in the window
}

// analyse pairs the punches around [from, to] (two days of margin on each
// side so shifts crossing the edges pair correctly) and keeps the automatic
// correction proposals in step: new anomalies get a proposal, proposals
// whose anomaly disappeared become obsolete. Must run in a transaction
// holding the employee lock.
func (s *Service) analyse(ctx context.Context, tx pgx.Tx, employeeID int64, from, to time.Time) (Analysis, error) {
	start, end := localMidnight(from.AddDate(0, 0, -2)), localMidnight(to.AddDate(0, 0, 3))
	ps, err := effectivePunches(ctx, tx, employeeID, start, end)
	if err != nil {
		return Analysis{}, err
	}
	h, err := leave.LoadHolidays(ctx, tx, from.AddDate(0, 0, -2), to.AddDate(0, 0, 2))
	if err != nil {
		return Analysis{}, err
	}
	sessions, anomalies := Pair(ps, s.now(), s.rules)
	a := Analysis{From: from, To: to, Sessions: Finalise(sessions, h, s.rules)}
	keys := []string{}
	for _, an := range anomalies {
		if an.WorkDate.Before(from) || an.WorkDate.After(to) {
			continue
		}
		a.Anomalies = append(a.Anomalies, an)
		keys = append(keys, an.Key)
		if _, err := tx.Exec(ctx, `
			INSERT INTO punch_corrections (employee_id, suggestion_key, kind, target_punch_id, proposed_at, work_date, reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (employee_id, suggestion_key) DO UPDATE
			   SET status = 'proposed', proposed_at = EXCLUDED.proposed_at, reason = EXCLUDED.reason
			 WHERE punch_corrections.status = 'obsolete'`,
			employeeID, an.Key, an.Kind, an.PunchID, an.SuggestedAt, an.WorkDate, an.Reason); err != nil {
			return Analysis{}, err
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE punch_corrections SET status = 'obsolete'
		 WHERE employee_id = $1 AND status = 'proposed' AND work_date BETWEEN $2 AND $3 AND suggestion_key <> ALL($4)`,
		employeeID, from, to, keys)
	return a, err
}

// Days returns sessions, anomalies and proposals for a date range.
func (s *Service) Days(ctx context.Context, employeeID int64, from, to time.Time) (Analysis, error) {
	var a Analysis
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockEmployee(ctx, tx, employeeID); err != nil {
			return err
		}
		var err error
		a, err = s.analyse(ctx, tx, employeeID, from, to)
		return err
	})
	return a, err
}

// MonthSummary is a month's weekly breakdown and totals.
type MonthSummary struct {
	EmployeeID     int64      `json:"employee_id"`
	Month          string     `json:"month"`
	From           string     `json:"from"`
	To             string     `json:"to"`
	Status         string     `json:"status"` // open | approved
	RuleVersion    string     `json:"rule_version"`
	Weeks          []Week     `json:"weeks"`
	Totals         Totals     `json:"totals"`
	OpenAnomalies  int        `json:"open_anomalies"`
	YearOvertime   int        `json:"year_overtime_minutes"` // approved months of the year + this one
	OvertimeCapHit bool       `json:"overtime_cap_exceeded"`
	Multipliers    multiplier `json:"multipliers_pct"`
	ApprovedBy     *int64     `json:"approved_by,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
}

type multiplier struct {
	Overtime int `json:"overtime"`
	RestDay  int `json:"rest_day"`
	Holiday  int `json:"holiday"`
}

func (s *Service) summarise(ctx context.Context, tx pgx.Tx, employeeID int64, month time.Time) (MonthSummary, error) {
	first, last := MonthRange(month)
	sum := MonthSummary{
		EmployeeID: employeeID, Month: month.Format("2006-01"), From: first.Format("2006-01-02"), To: last.Format("2006-01-02"),
		Status: "open", RuleVersion: s.rules.Version,
		Multipliers: multiplier{s.rules.OvertimePct, s.rules.RestDayPct, s.rules.HolidayPct},
	}

	var snapshot []byte
	err := tx.QueryRow(ctx, `
		SELECT totals, rule_version, approved_by, approved_at FROM timesheet_periods WHERE employee_id = $1 AND month = $2`,
		employeeID, month).Scan(&snapshot, &sum.RuleVersion, &sum.ApprovedBy, &sum.ApprovedAt)
	switch {
	case err == nil:
		// Approved months are frozen: report the snapshot, not a recomputation.
		var frozen MonthSummary
		if err := json.Unmarshal(snapshot, &frozen); err != nil {
			return MonthSummary{}, err
		}
		frozen.Status, frozen.ApprovedBy, frozen.ApprovedAt = "approved", sum.ApprovedBy, sum.ApprovedAt
		return frozen, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return MonthSummary{}, err
	}

	a, err := s.analyse(ctx, tx, employeeID, first, last)
	if err != nil {
		return MonthSummary{}, err
	}
	for _, w := range Weekly(a.Sessions, s.rules) {
		if !w.Start.Before(first) && !w.End.After(last) {
			sum.Weeks = append(sum.Weeks, w)
		}
	}
	if sum.Weeks == nil {
		sum.Weeks = []Week{}
	}
	sum.Totals = Sum(sum.Weeks)
	sum.OpenAnomalies = len(a.Anomalies)

	var earlier int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum((totals -> 'totals' ->> 'overtime_minutes')::int), 0) FROM timesheet_periods
		 WHERE employee_id = $1 AND date_trunc('year', month) = date_trunc('year', $2::date) AND month < $2`,
		employeeID, month).Scan(&earlier); err != nil {
		return MonthSummary{}, err
	}
	sum.YearOvertime = earlier + sum.Totals.OvertimeMinutes
	sum.OvertimeCapHit = sum.YearOvertime > int(s.rules.AnnualOvertimeCap/time.Minute)
	return sum, nil
}

// Month returns the summary of a month (live, or the frozen snapshot).
func (s *Service) Month(ctx context.Context, employeeID int64, month time.Time) (MonthSummary, error) {
	var sum MonthSummary
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockEmployee(ctx, tx, employeeID); err != nil {
			return err
		}
		var err error
		sum, err = s.summarise(ctx, tx, employeeID, month)
		return err
	})
	return sum, err
}

// Actor identifies who performs a write, for the audit log.
type Actor struct {
	UserID    int64
	Role      string
	RequestID string
	IP        string
}

func (a Actor) entry(action, entity, id string, oldV, newV any) audit.Entry {
	return audit.Entry{ActorUserID: &a.UserID, ActorRole: a.Role, Action: action, EntityType: entity, EntityID: id,
		Old: oldV, New: newV, RequestID: a.RequestID, IP: a.IP}
}

// ApproveMonth freezes a finished month with no open anomalies. Payroll
// reads the snapshot; nothing in the month can change afterwards.
func (s *Service) ApproveMonth(ctx context.Context, actor Actor, employeeID int64, month time.Time) (MonthSummary, error) {
	var sum MonthSummary
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockEmployee(ctx, tx, employeeID); err != nil {
			return err
		}
		_, last := MonthRange(month)
		if !last.Before(s.Today()) {
			return ErrPeriodOpen
		}
		var err error
		sum, err = s.summarise(ctx, tx, employeeID, month)
		if err != nil {
			return err
		}
		if sum.Status == "approved" {
			return ErrPeriodLocked
		}
		if sum.OpenAnomalies > 0 {
			return ErrUnresolved
		}
		snapshot, err := json.Marshal(sum)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO timesheet_periods (employee_id, month, rule_version, totals, approved_by) VALUES ($1, $2, $3, $4, $5)`,
			employeeID, month, s.rules.Version, snapshot, actor.UserID); err != nil {
			return err
		}
		sum.Status = "approved"
		return audit.Write(ctx, tx, actor.entry("timesheet.month_approve", "employee", strconv.FormatInt(employeeID, 10),
			nil, map[string]any{"month": sum.Month, "totals": sum.Totals, "rule_version": sum.RuleVersion}))
	})
	return sum, err
}

// periodApproved reports whether the payroll month containing day d is frozen.
func periodApproved(ctx context.Context, tx pgx.Tx, employeeID int64, d time.Time) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM timesheet_periods WHERE employee_id = $1 AND month = $2)`,
		employeeID, MonthOf(d)).Scan(&ok)
	return ok, err
}

// NewPunch is one punch to import.
type NewPunch struct {
	EmployeeID int64
	At         time.Time
	Dir        Direction
	Source     string // device | web | manual
	DeviceID   string
}

// Import stores punches. Device retries of an identical punch are ignored
// (unique index); punches into an approved month are refused as a whole.
func (s *Service) Import(ctx context.Context, actor Actor, punches []NewPunch) (inserted, duplicates int, err error) {
	err = db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		locked := map[int64]bool{}
		for _, p := range punches {
			if !locked[p.EmployeeID] {
				if err := lockEmployee(ctx, tx, p.EmployeeID); err != nil {
					return err
				}
				locked[p.EmployeeID] = true
			}
			if ok, err := periodApproved(ctx, tx, p.EmployeeID, CivilDate(p.At)); err != nil {
				return err
			} else if ok {
				return ErrPeriodLocked
			}
			var device *string
			if p.DeviceID != "" {
				device = &p.DeviceID
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO time_punches (employee_id, punched_at, direction, source, device_id, created_by)
				VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`,
				p.EmployeeID, p.At, p.Dir, p.Source, device, actor.UserID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				inserted++
			} else {
				duplicates++
			}
		}
		return audit.Write(ctx, tx, actor.entry("timesheet.punch_import", "time_punches", "",
			nil, map[string]int{"inserted": inserted, "duplicates": duplicates}))
	})
	return inserted, duplicates, err
}

// Correction is a proposal as returned by the API.
type Correction struct {
	ID            int64      `json:"id"`
	EmployeeID    int64      `json:"employee_id"`
	EmployeeName  string     `json:"employee_name"`
	Key           string     `json:"suggestion_key"`
	Kind          string     `json:"kind"`
	TargetPunchID int64      `json:"target_punch_id"`
	TargetAt      time.Time  `json:"target_punch_at"`
	TargetDir     string     `json:"target_direction"`
	ProposedAt    *time.Time `json:"proposed_at"`
	WorkDate      string     `json:"work_date"`
	Reason        string     `json:"reason"`
	Status        string     `json:"status"`
	AppliedAt     *time.Time `json:"applied_at"`
	DecisionNote  string     `json:"decision_note"`
	DecidedBy     *int64     `json:"decided_by"`
}

const correctionSelect = `
	SELECT c.id, c.employee_id, e.first_name || ' ' || e.last_name, c.suggestion_key, c.kind,
	       c.target_punch_id, p.punched_at, p.direction, c.proposed_at, to_char(c.work_date, 'YYYY-MM-DD'),
	       c.reason, c.status, c.applied_at, c.decision_note, c.decided_by
	  FROM punch_corrections c
	  JOIN time_punches p ON p.id = c.target_punch_id
	  JOIN employees e ON e.id = c.employee_id
	  JOIN org_units u ON u.id = e.org_unit_id`

func scanCorrection(row pgx.Row) (Correction, error) {
	var c Correction
	err := row.Scan(&c.ID, &c.EmployeeID, &c.EmployeeName, &c.Key, &c.Kind, &c.TargetPunchID, &c.TargetAt, &c.TargetDir,
		&c.ProposedAt, &c.WorkDate, &c.Reason, &c.Status, &c.AppliedAt, &c.DecisionNote, &c.DecidedBy)
	return c, err
}

// GetCorrection loads one proposal.
func (s *Service) GetCorrection(ctx context.Context, id int64) (Correction, error) {
	return scanCorrection(s.pool.QueryRow(ctx, correctionSelect+` WHERE c.id = $1`, id))
}

// DecideCorrection approves or rejects a proposal. Approval applies it: a
// drop hides the duplicate punch, an addition inserts a correction punch
// (at the proposed time, or at override when the approver knows better,
// within 12 hours of the proposal). The anomaly is re-checked first: a
// proposal overtaken by later punches is marked obsolete instead.
func (s *Service) DecideCorrection(ctx context.Context, actor Actor, id int64, approve bool, override *time.Time, note string,
	authorise func(Correction) error) (Correction, error) {
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Lock order everywhere: employee advisory lock, then rows. analyse
		// updates proposals under the employee lock, so taking the row lock
		// first here could deadlock with a concurrent read.
		var employeeID int64
		if err := tx.QueryRow(ctx, `SELECT employee_id FROM punch_corrections WHERE id = $1`, id).Scan(&employeeID); err != nil {
			return err
		}
		if err := lockEmployee(ctx, tx, employeeID); err != nil {
			return err
		}
		c, err := scanCorrection(tx.QueryRow(ctx, correctionSelect+` WHERE c.id = $1 FOR UPDATE OF c`, id))
		if err != nil {
			return err
		}
		if err := authorise(c); err != nil {
			return err
		}
		if c.Status != "proposed" {
			return ErrAlreadyDecided
		}
		workDate, _ := time.Parse("2006-01-02", c.WorkDate)
		if ok, err := periodApproved(ctx, tx, c.EmployeeID, workDate); err != nil {
			return err
		} else if ok {
			return ErrPeriodLocked
		}
		a, err := s.analyse(ctx, tx, c.EmployeeID, workDate, workDate)
		if err != nil {
			return err
		}
		current := false
		for _, an := range a.Anomalies {
			current = current || an.Key == c.Key
		}
		if !current {
			return ErrAlreadyDecided // analyse has just marked it obsolete
		}

		status, applied := "rejected", (*time.Time)(nil)
		if approve {
			status = "approved"
			if c.Kind != string(Duplicate) {
				t := *c.ProposedAt
				if override != nil {
					if d := override.Sub(t); d > 12*time.Hour || d < -12*time.Hour {
						return errOverrideRange
					}
					t = override.Truncate(time.Minute)
				}
				applied = &t
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE punch_corrections SET status = $2, applied_at = $3, decision_note = $4, decided_by = $5, decided_at = now()
			 WHERE id = $1`, id, status, applied, note, actor.UserID); err != nil {
			return err
		}
		if applied != nil {
			dir := Out
			if c.Kind == string(MissingIn) {
				dir = In
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO time_punches (employee_id, punched_at, direction, source, correction_id, created_by)
				VALUES ($1, $2, $3, 'correction', $4, $5)`, c.EmployeeID, *applied, dir, id, actor.UserID); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor.entry("timesheet.correction_"+status, "punch_correction", strconv.FormatInt(id, 10),
			map[string]string{"status": c.Status}, map[string]any{"status": status, "applied_at": applied, "note": note}))
	})
	if err != nil {
		return Correction{}, err
	}
	return s.GetCorrection(ctx, id)
}

var errOverrideRange = errors.New("override must be within 12 hours of the proposal")
