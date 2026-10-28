package payroll

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/audit"
	"bordroplus/internal/db"
)

// Item kinds. A bonus is a gross addition subject to SGK and income tax. An
// advance (paid out by accounting beforehand) and a loan are recovered from
// net pay: the advance at once, the loan in equal monthly instalments.
const (
	KindBonus   = "bonus"
	KindAdvance = "advance"
	KindLoan    = "loan"
)

var (
	ErrDeductionCap = errors.New("instalment exceeds a quarter of the monthly gross salary")
	ErrBadItem      = errors.New("invalid payroll item")
)

// Instalments splits total into n monthly amounts: equal kuruş, the
// remainder on the last one, so the parts always add up to the total.
func Instalments(total Kurus, n int) []Kurus {
	out := make([]Kurus, n)
	each := total / Kurus(n)
	for i := range out {
		out[i] = each
	}
	out[n-1] += total - each*Kurus(n)
	return out
}

// PayrollItem is an item as returned by the API.
type PayrollItem struct {
	ID          int64          `json:"id"`
	EmployeeID  int64          `json:"employee_id"`
	Kind        string         `json:"kind"`
	Label       string         `json:"label"`
	Amount      Kurus          `json:"amount"`
	Instalments int            `json:"instalments"`
	StartPeriod string         `json:"start_period"`
	Schedule    []ScheduleLine `json:"schedule"`
	CreatedBy   int64          `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	CancelledAt *time.Time     `json:"cancelled_at"`
}

type ScheduleLine struct {
	Period string `json:"period"`
	Amount Kurus  `json:"amount"`
}

const itemSelect = `
	SELECT id, employee_id, kind, label, amount, instalments, start_period, created_by, created_at, cancelled_at
	  FROM payroll_items`

func scanItem(row pgx.Row) (PayrollItem, error) {
	var it PayrollItem
	var start time.Time
	err := row.Scan(&it.ID, &it.EmployeeID, &it.Kind, &it.Label, &it.Amount, &it.Instalments, &start,
		&it.CreatedBy, &it.CreatedAt, &it.CancelledAt)
	if err != nil {
		return PayrollItem{}, err
	}
	it.StartPeriod = start.Format("2006-01")
	for i, a := range Instalments(it.Amount, it.Instalments) {
		it.Schedule = append(it.Schedule, ScheduleLine{Period: start.AddDate(0, i, 0).Format("2006-01"), Amount: a})
	}
	return it, nil
}

// itemsFor returns the engine items an employee has in period.
func itemsFor(ctx context.Context, q db.Querier, employeeID int64, period time.Time) ([]Item, error) {
	rows, err := q.Query(ctx, itemSelect+`
		 WHERE employee_id = $1 AND cancelled_at IS NULL
		   AND start_period <= $2 AND start_period + (instalments - 1) * interval '1 month' >= $2
		 ORDER BY id`, employeeID, period)
	if err != nil {
		return nil, err
	}
	stored, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (PayrollItem, error) { return scanItem(row) })
	if err != nil {
		return nil, err
	}
	items := []Item{}
	for _, it := range stored {
		id := strconv.FormatInt(it.ID, 10)
		switch it.Kind {
		case KindBonus:
			items = append(items, Item{Code: "bonus_" + id, Label: it.Label, Amount: it.Amount, Earning: true, SGK: true, Tax: true})
		case KindAdvance:
			items = append(items, Item{Code: "advance_" + id, Label: it.Label, Amount: it.Amount})
		case KindLoan:
			for k, line := range it.Schedule {
				if line.Period == period.Format("2006-01") {
					items = append(items, Item{Code: fmt.Sprintf("loan_%s_%d", id, k+1),
						Label: fmt.Sprintf("%s (%d/%d)", it.Label, k+1, it.Instalments), Amount: line.Amount})
				}
			}
		}
	}
	return items, nil
}

// checkSchedulePeriods refuses an item touching a period that is under
// review, approved or closed: those payrolls must not change.
func checkSchedulePeriods(ctx context.Context, tx pgx.Tx, start time.Time, n int) error {
	var frozenAny bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM payroll_periods
		                WHERE status IN ('review', 'approved', 'closed')
		                  AND period BETWEEN $1 AND $1 + ($2 - 1) * interval '1 month')`, start, n).Scan(&frozenAny)
	if err != nil {
		return err
	}
	if frozenAny {
		return ErrPeriodFrozen
	}
	return nil
}

// NewItem is validated input for CreateItem.
type NewItem struct {
	EmployeeID  int64
	Kind        string
	Label       string
	Amount      Kurus
	Instalments int
	Start       time.Time
}

// CreateItem records a bonus, advance or loan. A deduction instalment may
// not exceed a quarter of the gross salary in force at the start (company
// policy mirroring the Labour Law art. 35 ratio), so net pay cannot go
// negative.
func (s *Service) CreateItem(ctx context.Context, actor Actor, in NewItem) (PayrollItem, error) {
	in.Start = MonthStart(in.Start)
	var id int64
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockEmployee(ctx, tx, in.EmployeeID); err != nil {
			return err
		}
		if err := checkSchedulePeriods(ctx, tx, in.Start, in.Instalments); err != nil {
			return err
		}
		if in.Kind != KindBonus {
			sal, err := s.salaries(ctx, tx, in.EmployeeID, in.Start.AddDate(0, 1, -1))
			if err != nil {
				return err
			}
			if len(sal) == 0 {
				return ErrNoSalary
			}
			if Instalments(in.Amount, in.Instalments)[in.Instalments-1]*4 > sal[len(sal)-1].amount {
				return ErrDeductionCap
			}
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO payroll_items (employee_id, kind, label, amount, instalments, start_period, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			in.EmployeeID, in.Kind, in.Label, in.Amount, in.Instalments, in.Start, actor.UserID).Scan(&id)
		if db.PgCode(err) == "23503" {
			return ErrNotEmployed
		}
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorUserID: &actor.UserID, ActorRole: actor.Role, Action: "payroll.item_create", EntityType: "payroll_item",
			EntityID: strconv.FormatInt(id, 10), RequestID: actor.RequestID, IP: actor.IP,
			New: map[string]any{"employee_id": in.EmployeeID, "kind": in.Kind, "label": in.Label, "amount": in.Amount,
				"instalments": in.Instalments, "start_period": in.Start.Format("2006-01")},
		})
	})
	if err != nil {
		return PayrollItem{}, err
	}
	return scanItem(s.pool.QueryRow(ctx, itemSelect+` WHERE id = $1`, id))
}

// CancelItem withdraws an item that has not reached a frozen payroll.
func (s *Service) CancelItem(ctx context.Context, actor Actor, id int64) (PayrollItem, error) {
	err := db.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		it, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if it.CancelledAt != nil {
			return fmt.Errorf("%w: already cancelled", ErrBadItem)
		}
		start, _ := time.Parse("2006-01", it.StartPeriod)
		if err := checkSchedulePeriods(ctx, tx, start, it.Instalments); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payroll_items SET cancelled_by = $2, cancelled_at = now() WHERE id = $1`, id, actor.UserID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorUserID: &actor.UserID, ActorRole: actor.Role, Action: "payroll.item_cancel", EntityType: "payroll_item",
			EntityID: strconv.FormatInt(id, 10), RequestID: actor.RequestID, IP: actor.IP,
		})
	})
	if err != nil {
		return PayrollItem{}, err
	}
	return scanItem(s.pool.QueryRow(ctx, itemSelect+` WHERE id = $1`, id))
}

// ListItems lists items, optionally for one employee and/or one period.
func (s *Service) ListItems(ctx context.Context, employeeID *int64, period *time.Time) ([]PayrollItem, error) {
	rows, err := s.pool.Query(ctx, itemSelect+`
		 WHERE ($1::bigint IS NULL OR employee_id = $1)
		   AND ($2::date IS NULL OR (start_period <= $2 AND start_period + (instalments - 1) * interval '1 month' >= $2))
		 ORDER BY id DESC LIMIT 500`, employeeID, period)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (PayrollItem, error) { return scanItem(row) })
}
