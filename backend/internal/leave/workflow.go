package leave

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/audit"
	"bordroplus/internal/db"
)

// Leave types. Only annual leave is deducted from the balance; an approved
// sick report refunds overlapping annual leave.
const (
	TypeAnnual = "annual"
	TypeSick   = "sick"
	TypeUnpaid = "unpaid"
	TypeExcuse = "excuse"
)

var validTypes = map[string]bool{TypeAnnual: true, TypeSick: true, TypeUnpaid: true, TypeExcuse: true}

// Request is a leave request as returned by the API.
type Request struct {
	ID           int64      `json:"id"`
	EmployeeID   int64      `json:"employee_id"`
	EmployeeName string     `json:"employee_name"`
	Type         string     `json:"leave_type"`
	StartDate    string     `json:"start_date"`
	EndDate      string     `json:"end_date"`
	HalfDay      HalfDay    `json:"half_day"`
	Days         float64    `json:"days"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason"`
	DecisionNote string     `json:"decision_note"`
	RequestedBy  int64      `json:"requested_by"`
	DecidedBy    *int64     `json:"decided_by"`
	DecidedAt    *time.Time `json:"decided_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

const requestSelect = `
	SELECT r.id, r.employee_id, e.first_name || ' ' || e.last_name, r.leave_type,
	       to_char(r.start_date, 'YYYY-MM-DD'), to_char(r.end_date, 'YYYY-MM-DD'), r.half_day, r.half_days,
	       r.status, r.reason, r.decision_note, r.requested_by, r.decided_by, r.decided_at, r.created_at
	  FROM leave_requests r
	  JOIN employees e ON e.id = r.employee_id
	  JOIN org_units u ON u.id = e.org_unit_id`

func scanRequest(row pgx.Row) (Request, error) {
	var r Request
	var half int
	err := row.Scan(&r.ID, &r.EmployeeID, &r.EmployeeName, &r.Type, &r.StartDate, &r.EndDate, &r.HalfDay, &half,
		&r.Status, &r.Reason, &r.DecisionNote, &r.RequestedBy, &r.DecidedBy, &r.DecidedAt, &r.CreatedAt)
	r.Days = Days(half)
	return r, err
}

// NewRequest is validated input for Create.
type NewRequest struct {
	EmployeeID int64
	Type       string
	Start, End time.Time
	Half       HalfDay
	Reason     string
}

// Actor identifies who performs a workflow step, for the audit log.
type Actor struct {
	UserID    int64
	Role      string
	RequestID string
	IP        string
}

func (a Actor) entry(action string, requestID int64, oldV, newV any) audit.Entry {
	return audit.Entry{
		ActorUserID: &a.UserID, ActorRole: a.Role, Action: action,
		EntityType: "leave_request", EntityID: strconv.FormatInt(requestID, 10),
		Old: oldV, New: newV, RequestID: a.RequestID, IP: a.IP,
	}
}

// Create files a pending request. Annual leave must fit the available
// balance (balance minus other pending annual requests); the check is
// repeated, under lock, at approval.
func (s *Service) Create(ctx context.Context, actor Actor, in NewRequest) (Request, error) {
	var id int64
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		cost, err := Cost(ctx, tx, in.Start, in.End, in.Half)
		if err != nil {
			return err
		}
		if cost == 0 && (in.Type == TypeAnnual || in.Type == TypeExcuse) {
			return ErrNoWorkingDays
		}
		// Insert first, lock the account second: approvals lock request then
		// account, so both paths take the account lock last and cannot deadlock.
		err = tx.QueryRow(ctx, `
			INSERT INTO leave_requests (employee_id, leave_type, start_date, end_date, half_day, half_days, reason, requested_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			in.EmployeeID, in.Type, in.Start, in.End, in.Half, cost, in.Reason, actor.UserID).Scan(&id)
		if db.PgCode(err) == "23P01" {
			return ErrOverlap
		}
		if err != nil {
			return err
		}
		if in.Type == TypeAnnual {
			balance, err := lockAccount(ctx, tx, in.EmployeeID, s.Today())
			if err != nil {
				return err
			}
			pending, err := pendingAnnual(ctx, tx, in.EmployeeID, id)
			if err != nil {
				return err
			}
			if cost > balance-pending {
				return ErrInsufficientBalance
			}
		}
		return audit.Write(ctx, tx, actor.entry("leave.request", id, nil, map[string]any{
			"employee_id": in.EmployeeID, "leave_type": in.Type, "start_date": in.Start.Format("2006-01-02"),
			"end_date": in.End.Format("2006-01-02"), "half_day": in.Half, "days": Days(cost),
		}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.Get(ctx, id)
}

// Get loads one request.
func (s *Service) Get(ctx context.Context, id int64) (Request, error) {
	return scanRequest(s.pool.QueryRow(ctx, requestSelect+` WHERE r.id = $1`, id))
}

// lockRequest loads a request and locks its row for the transaction.
func lockRequest(ctx context.Context, tx pgx.Tx, id int64) (Request, error) {
	r, err := scanRequest(tx.QueryRow(ctx, requestSelect+` WHERE r.id = $1 FOR UPDATE OF r`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, pgx.ErrNoRows
	}
	return r, err
}

// Approve books an approved request. For annual leave the cost is
// recomputed (holidays may have been added since the request) and deducted
// under the account lock: two approvals racing for the last days of one
// employee are serialised and the loser gets ErrInsufficientBalance.
// authorise is called with the locked request before anything changes.
func (s *Service) Approve(ctx context.Context, actor Actor, id int64, note string, authorise func(Request) error) (Request, error) {
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		r, err := lockRequest(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authorise(r); err != nil {
			return err
		}
		if r.Status != "pending" {
			return ErrNotPending
		}
		start, _ := time.Parse("2006-01-02", r.StartDate)
		end, _ := time.Parse("2006-01-02", r.EndDate)
		cost, err := Cost(ctx, tx, start, end, r.HalfDay)
		if err != nil {
			return err
		}
		if r.Type == TypeAnnual {
			balance, err := lockAccount(ctx, tx, r.EmployeeID, s.Today())
			if err != nil {
				return err
			}
			if cost > balance {
				return ErrInsufficientBalance
			}
			if cost > 0 {
				if _, err := tx.Exec(ctx, `
					INSERT INTO leave_ledger (employee_id, kind, half_days, effective_date, leave_request_id, note, created_by)
					VALUES ($1, 'usage', $2, $3, $4, 'yıllık izin kullanımı', $5)`,
					r.EmployeeID, -cost, start, id, actor.UserID); err != nil {
					return err
				}
				if err := addBalance(ctx, tx, r.EmployeeID, -cost); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE leave_requests SET status = 'approved', half_days = $2, decision_note = $3, decided_by = $4, decided_at = now()
			 WHERE id = $1`, id, cost, note, actor.UserID); err != nil {
			return err
		}
		if r.Type == TypeAnnual || r.Type == TypeSick {
			if _, err := lockAccount(ctx, tx, r.EmployeeID, s.Today()); err != nil {
				return err
			}
			if _, err := applySickRefunds(ctx, tx, r.EmployeeID, actor.UserID); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, actor.entry("leave.approve", id,
			map[string]string{"status": r.Status}, map[string]any{"status": "approved", "days": Days(cost), "note": note}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.Get(ctx, id)
}

// Reject closes a pending request without touching the balance.
func (s *Service) Reject(ctx context.Context, actor Actor, id int64, note string, authorise func(Request) error) (Request, error) {
	return s.decide(ctx, actor, id, "rejected", "leave.reject", note, authorise)
}

// Cancel withdraws a request. Cancelling approved annual leave reverses
// what is still charged (usage minus sick refunds).
func (s *Service) Cancel(ctx context.Context, actor Actor, id int64, authorise func(Request) error) (Request, error) {
	return s.decide(ctx, actor, id, "cancelled", "leave.cancel", "", authorise)
}

func (s *Service) decide(ctx context.Context, actor Actor, id int64, status, action, note string, authorise func(Request) error) (Request, error) {
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		r, err := lockRequest(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authorise(r); err != nil {
			return err
		}
		switch {
		case r.Status == "pending":
		// Approved sick reports stay: refunds already granted depend on them.
		case r.Status == "approved" && status == "cancelled" && r.Type != TypeSick:
			if r.Type == TypeAnnual {
				if _, err := lockAccount(ctx, tx, r.EmployeeID, s.Today()); err != nil {
					return err
				}
				var charged int
				if err := tx.QueryRow(ctx, `
					SELECT COALESCE(-sum(half_days), 0) FROM leave_ledger
					 WHERE leave_request_id = $1 AND kind IN ('usage', 'sick_refund')`, id).Scan(&charged); err != nil {
					return err
				}
				if charged > 0 {
					if _, err := tx.Exec(ctx, `
						INSERT INTO leave_ledger (employee_id, kind, half_days, effective_date, leave_request_id, note, created_by)
						VALUES ($1, 'reversal', $2, $3, $4, 'onaylı izin iptal edildi', $5)`,
						r.EmployeeID, charged, s.Today(), id, actor.UserID); err != nil {
						return err
					}
					if err := addBalance(ctx, tx, r.EmployeeID, charged); err != nil {
						return err
					}
				}
			}
		default:
			return ErrNotPending
		}
		if _, err := tx.Exec(ctx, `
			UPDATE leave_requests SET status = $2, decision_note = $3, decided_by = $4, decided_at = now() WHERE id = $1`,
			id, status, note, actor.UserID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor.entry(action, id, map[string]string{"status": r.Status}, map[string]string{"status": status, "note": note}))
	})
	if err != nil {
		return Request{}, err
	}
	return s.Get(ctx, id)
}
