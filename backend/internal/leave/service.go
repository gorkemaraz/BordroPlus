package leave

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/db"
)

// Errors returned by the service; the HTTP layer maps them to responses.
var (
	ErrInsufficientBalance = errors.New("insufficient leave balance")
	ErrOverlap             = errors.New("overlapping leave request")
	ErrNotPending          = errors.New("request is not pending")
	ErrNoWorkingDays       = errors.New("no working days in range")
)

// Service owns leave balances and requests.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool, now: time.Now} }

var istanbul = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		panic(err)
	}
	return loc
}()

// Today is the current civil date in Turkey.
func (s *Service) Today() time.Time {
	y, m, d := s.now().In(istanbul).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// holidays loads public holidays in [from, to].
func holidays(ctx context.Context, q db.Querier, from, to time.Time) (Holidays, error) {
	rows, err := q.Query(ctx, `SELECT holiday_date, half_day FROM public_holidays WHERE holiday_date BETWEEN $1 AND $2`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	h := Holidays{}
	for rows.Next() {
		var d time.Time
		var half bool
		if err := rows.Scan(&d, &half); err != nil {
			return nil, err
		}
		h[d] = half
	}
	return h, rows.Err()
}

// Cost computes the working-time cost of a range from the holiday table.
func Cost(ctx context.Context, q db.Querier, start, end time.Time, half HalfDay) (int, error) {
	h, err := holidays(ctx, q, start, end)
	if err != nil {
		return 0, err
	}
	return WorkingHalfDays(start, end, half, h)
}

// lockAccount creates the employee's leave account if needed, locks it for
// the rest of the transaction, books any anniversary accruals up to asOf
// and returns the balance. Every balance change goes through here, so
// concurrent writers for one employee serialise on this row lock.
func lockAccount(ctx context.Context, tx pgx.Tx, employeeID int64, asOf time.Time) (int, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO leave_accounts (employee_id) VALUES ($1) ON CONFLICT DO NOTHING`, employeeID); err != nil {
		return 0, err
	}
	var balance int
	if err := tx.QueryRow(ctx, `SELECT balance_half_days FROM leave_accounts WHERE employee_id = $1 FOR UPDATE`, employeeID).
		Scan(&balance); err != nil {
		return 0, err
	}

	var hire time.Time
	var birth, term *time.Time
	if err := tx.QueryRow(ctx, `SELECT hire_date, birth_date, termination_date FROM employees WHERE id = $1`, employeeID).
		Scan(&hire, &birth, &term); err != nil {
		return 0, err
	}
	acc := Accruals(hire, birth, term, asOf)
	if len(acc) == 0 {
		return balance, nil
	}
	dates := make([]time.Time, len(acc))
	amounts := make([]int32, len(acc))
	notes := make([]string, len(acc))
	for i, a := range acc {
		dates[i], amounts[i] = a.Date, int32(a.HalfDays)
		notes[i] = fmt.Sprintf("%d. yıl hakedişi (%d gün)", a.ServiceYears, a.HalfDays/2)
	}
	var added int
	err := tx.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO leave_ledger (employee_id, kind, half_days, effective_date, note)
			SELECT $1, 'accrual', a, d, n FROM unnest($2::date[], $3::int[], $4::text[]) AS t(d, a, n)
			ON CONFLICT (employee_id, effective_date) WHERE kind = 'accrual' DO NOTHING
			RETURNING half_days)
		SELECT COALESCE(sum(half_days), 0) FROM ins`, employeeID, dates, amounts, notes).Scan(&added)
	if err != nil {
		return 0, err
	}
	if added > 0 {
		if err := addBalance(ctx, tx, employeeID, added); err != nil {
			return 0, err
		}
		balance += added
	}
	return balance, nil
}

func addBalance(ctx context.Context, tx pgx.Tx, employeeID int64, delta int) error {
	_, err := tx.Exec(ctx, `
		UPDATE leave_accounts SET balance_half_days = balance_half_days + $2, updated_at = now() WHERE employee_id = $1`,
		employeeID, delta)
	if db.PgCode(err) == "23514" { // CHECK (balance_half_days >= 0)
		return ErrInsufficientBalance
	}
	return err
}

// pendingAnnual is the cost of the employee's pending annual requests.
func pendingAnnual(ctx context.Context, q db.Querier, employeeID int64, exclude int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		SELECT COALESCE(sum(half_days), 0) FROM leave_requests
		 WHERE employee_id = $1 AND leave_type = 'annual' AND status = 'pending' AND id <> $2`, employeeID, exclude).Scan(&n)
	return n, err
}

// applySickRefunds gives back annual leave days covered by approved sick
// reports. It is called after every approval, so the result does not
// depend on which of the two was approved first; the unique index on
// (leave_request_id, related_request_id) makes it idempotent.
func applySickRefunds(ctx context.Context, tx pgx.Tx, employeeID int64, actor int64) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT a.id, a.start_date, a.end_date, a.half_day, a.half_days,
		       COALESCE((SELECT sum(l.half_days) FROM leave_ledger l
		                  WHERE l.leave_request_id = a.id AND l.kind = 'sick_refund'), 0),
		       s.id, s.start_date, s.end_date
		  FROM leave_requests a
		  JOIN leave_requests s
		    ON s.employee_id = a.employee_id AND s.leave_type = 'sick' AND s.status = 'approved'
		   AND daterange(s.start_date, s.end_date, '[]') && daterange(a.start_date, a.end_date, '[]')
		 WHERE a.employee_id = $1 AND a.leave_type = 'annual' AND a.status = 'approved'
		   AND NOT EXISTS (SELECT 1 FROM leave_ledger l
		                    WHERE l.kind = 'sick_refund' AND l.leave_request_id = a.id AND l.related_request_id = s.id)
		 ORDER BY a.id, s.id`, employeeID)
	if err != nil {
		return 0, err
	}
	type pair struct {
		annualID, sickID           int64
		aStart, aEnd, sStart, sEnd time.Time
		half                       HalfDay
		cost, refunded             int
	}
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.annualID, &p.aStart, &p.aEnd, &p.half, &p.cost, &p.refunded, &p.sickID, &p.sStart, &p.sEnd); err != nil {
			rows.Close()
			return 0, err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := 0
	refundedSoFar := map[int64]int{}
	for _, p := range pairs {
		h, err := holidays(ctx, tx, p.aStart, p.aEnd)
		if err != nil {
			return 0, err
		}
		already := p.refunded + refundedSoFar[p.annualID]
		r := min(SickRefund(p.aStart, p.aEnd, p.half, p.cost, p.sStart, p.sEnd, h), p.cost-already)
		if r <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO leave_ledger (employee_id, kind, half_days, effective_date, leave_request_id, related_request_id, note, created_by)
			VALUES ($1, 'sick_refund', $2, $3, $4, $5, 'izin sırasındaki rapor günleri iade edildi', $6)`,
			employeeID, r, p.sStart, p.annualID, p.sickID, actor); err != nil {
			return 0, err
		}
		refundedSoFar[p.annualID] += r
		total += r
	}
	if total > 0 {
		if err := addBalance(ctx, tx, employeeID, total); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// Balance is an employee's leave position. Day amounts are exact halves.
type Balance struct {
	EmployeeID     int64       `json:"employee_id"`
	AsOf           string      `json:"as_of"`
	HireDate       string      `json:"hire_date"`
	ServiceYears   int         `json:"service_years"`
	Balance        float64     `json:"balance"`         // booked, after every approved movement
	Pending        float64     `json:"pending"`         // pending annual requests
	Available      float64     `json:"available"`       // balance - pending
	CarriedOver    float64     `json:"carried_over"`    // balance before the latest accrual
	CurrentAccrual float64     `json:"current_accrual"` // days of the latest accrual
	TotalAccrued   float64     `json:"total_accrued"`
	TotalUsed      float64     `json:"total_used"`
	TotalRefunded  float64     `json:"total_refunded"` // sick refunds and cancellations
	NextAccrual    *NextAccrue `json:"next_accrual"`
}

type NextAccrue struct {
	Date string  `json:"date"`
	Days float64 `json:"days"`
}

// GetBalance books due accruals and summarises the ledger.
func (s *Service) GetBalance(ctx context.Context, employeeID int64) (Balance, error) {
	today := s.Today()
	var b Balance
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		bal, err := lockAccount(ctx, tx, employeeID, today)
		if err != nil {
			return err
		}
		pending, err := pendingAnnual(ctx, tx, employeeID, 0)
		if err != nil {
			return err
		}
		var accrued, used, refunded, carried, current int
		var lastAccrual *time.Time
		err = tx.QueryRow(ctx, `
			WITH last AS (SELECT max(effective_date) AS d FROM leave_ledger WHERE employee_id = $1 AND kind = 'accrual')
			SELECT COALESCE(sum(half_days) FILTER (WHERE kind = 'accrual'), 0),
			       COALESCE(-sum(half_days) FILTER (WHERE kind = 'usage'), 0),
			       COALESCE(sum(half_days) FILTER (WHERE kind IN ('sick_refund', 'reversal')), 0),
			       COALESCE(sum(half_days) FILTER (WHERE effective_date < (SELECT d FROM last)), 0),
			       COALESCE(sum(half_days) FILTER (WHERE kind = 'accrual' AND effective_date = (SELECT d FROM last)), 0),
			       (SELECT d FROM last)
			  FROM leave_ledger WHERE employee_id = $1`, employeeID).
			Scan(&accrued, &used, &refunded, &carried, &current, &lastAccrual)
		if err != nil {
			return err
		}
		var hire time.Time
		var birth, term *time.Time
		if err := tx.QueryRow(ctx, `SELECT hire_date, birth_date, termination_date FROM employees WHERE id = $1`, employeeID).
			Scan(&hire, &birth, &term); err != nil {
			return err
		}
		years := len(Accruals(hire, birth, term, today))
		b = Balance{
			EmployeeID: employeeID, AsOf: today.Format("2006-01-02"), HireDate: hire.Format("2006-01-02"),
			ServiceYears: years, Balance: Days(bal), Pending: Days(pending), Available: Days(bal - pending),
			CarriedOver: Days(carried), CurrentAccrual: Days(current),
			TotalAccrued: Days(accrued), TotalUsed: Days(used), TotalRefunded: Days(refunded),
		}
		if term == nil || Anniversary(hire, years+1).Before(*term) || Anniversary(hire, years+1).Equal(*term) {
			next := Anniversary(hire, years+1)
			age := -1
			if birth != nil {
				age = AgeOn(*birth, next)
			}
			b.NextAccrual = &NextAccrue{Date: next.Format("2006-01-02"), Days: float64(EntitlementDays(years+1, age))}
		}
		return nil
	})
	return b, err
}
