package org

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bordroplus/internal/audit"
	"bordroplus/internal/auth"
	"bordroplus/internal/db"
	"bordroplus/internal/httpx"
	"bordroplus/internal/money"
)

// Employee is the employee card. Salary is present only for callers allowed
// to see it (salary.read, or the employee themself); otherwise it is null and
// SalaryMasked is true.
type Employee struct {
	ID              int64      `json:"id"`
	EmployeeNo      string     `json:"employee_no"`
	FirstName       string     `json:"first_name"`
	LastName        string     `json:"last_name"`
	Title           string     `json:"title"`
	OrgUnitID       int64      `json:"org_unit_id"`
	OrgUnitName     string     `json:"org_unit_name"`
	OrgUnitPath     string     `json:"org_unit_path"`
	HireDate        string     `json:"hire_date"`
	TerminationDate *string    `json:"termination_date"`
	Salary          *SalaryRow `json:"salary,omitempty"`
	SalaryMasked    *bool      `json:"salary_masked,omitempty"`
}

// SalaryRow is one entry of an employee's salary history.
type SalaryRow struct {
	ValidFrom string `json:"valid_from"`
	Amount    string `json:"amount"` // monthly gross, TRY, "45000.00"
}

const employeeCols = `
	e.id, e.employee_no, e.first_name, e.last_name, e.title, e.org_unit_id, u.name, u.path::text,
	to_char(e.hire_date, 'YYYY-MM-DD'), to_char(e.termination_date, 'YYYY-MM-DD')`

// ScopeSQL restricts rows to the caller's scope (aliases: e = employees,
// u = org_units); args $1..$3 come from Scope.Args.
const ScopeSQL = `($1::bool OR u.path <@ ANY($2::ltree[]) OR e.id = $3)`

func scanEmployee(row pgx.Row) (Employee, error) {
	var e Employee
	err := row.Scan(&e.ID, &e.EmployeeNo, &e.FirstName, &e.LastName, &e.Title, &e.OrgUnitID,
		&e.OrgUnitName, &e.OrgUnitPath, &e.HireDate, &e.TerminationDate)
	return e, err
}

// listEmployees: keyset-paginated by id, limited to the caller's scope.
// Filters: q (name or employee number), org_unit_id (whole sub-tree),
// include_terminated=true.
func (h *Handler) listEmployees(w http.ResponseWriter, r *http.Request) error {
	scope := ScopeFor(principal(r), EmployeeData)
	if scope.Empty() {
		return httpx.ErrForbidden
	}
	limit, err := httpx.PageLimit(r, 50, 200)
	if err != nil {
		return err
	}
	cursor, err := queryID(r, "cursor")
	if err != nil {
		return err
	}
	unit, err := queryID(r, "org_unit_id")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("q"))
	if len(search) > 100 {
		return httpx.Validation(map[string]string{"q": "en fazla 100 karakter"})
	}
	inclTerminated := q.Get("include_terminated") == "true"

	args := append(scope.Args(), cursor, unit, search, inclTerminated, limit+1)
	rows, err := h.pool.Query(r.Context(), `
		SELECT `+employeeCols+`
		  FROM employees e
		  JOIN org_units u ON u.id = e.org_unit_id
		 WHERE `+ScopeSQL+`
		   AND ($4::bigint IS NULL OR e.id > $4)
		   AND ($5::bigint IS NULL OR u.path <@ (SELECT path FROM org_units WHERE id = $5))
		   AND ($6 = '' OR e.employee_no = upper($6)
		        OR lower(e.first_name || ' ' || e.last_name) LIKE '%' || lower($6) || '%')
		   AND ($7 OR e.termination_date IS NULL OR e.termination_date >= current_date)
		 ORDER BY e.id
		 LIMIT $8`, args...)
	if err != nil {
		return err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Employee, error) { return scanEmployee(row) })
	if err != nil {
		return err
	}
	page := httpx.Page[Employee]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = strconv.FormatInt(items[limit-1].ID, 10)
	}
	httpx.WriteJSON(w, http.StatusOK, page)
	return nil
}

// loadVisible returns the employee if it is inside the caller's scope.
// Unknown ids give 404, existing employees outside the scope 403.
func (h *Handler) loadVisible(ctx context.Context, p auth.Principal, id int64) (Employee, error) {
	var (
		e       Employee
		visible bool
	)
	args := append(ScopeFor(p, EmployeeData).Args(), id)
	err := h.pool.QueryRow(ctx, `
		SELECT `+employeeCols+`, `+ScopeSQL+`
		  FROM employees e JOIN org_units u ON u.id = e.org_unit_id
		 WHERE e.id = $4`, args...).
		Scan(&e.ID, &e.EmployeeNo, &e.FirstName, &e.LastName, &e.Title, &e.OrgUnitID,
			&e.OrgUnitName, &e.OrgUnitPath, &e.HireDate, &e.TerminationDate, &visible)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Employee{}, httpx.ErrNotFound
	case err != nil:
		return Employee{}, err
	case !visible:
		return Employee{}, errNotVisible
	}
	return e, nil
}

func (h *Handler) getEmployee(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p := principal(r)
	e, err := h.loadVisible(r.Context(), p, id)
	if err != nil {
		return err
	}
	if canSeeSalary(p, id) {
		e.Salary, err = h.currentSalary(r.Context(), id)
		if err != nil {
			return err
		}
	} else {
		masked := true
		e.SalaryMasked = &masked
	}
	httpx.WriteJSON(w, http.StatusOK, e)
	return nil
}

func canSeeSalary(p auth.Principal, employeeID int64) bool {
	return p.Can(auth.PermSalaryRead) || (p.EmployeeID != nil && *p.EmployeeID == employeeID)
}

type employeeInput struct {
	EmployeeNo      *string `json:"employee_no"`
	FirstName       *string `json:"first_name"`
	LastName        *string `json:"last_name"`
	Title           *string `json:"title"`
	OrgUnitID       *int64  `json:"org_unit_id"`
	HireDate        *string `json:"hire_date"`
	TerminationDate *string `json:"termination_date"`
	Salary          *struct {
		Amount    string `json:"amount"`
		ValidFrom string `json:"valid_from"`
	} `json:"salary"`
}

var employeeNo = regexp.MustCompile(`^[A-Z0-9-]{3,20}$`)

func trimmedLen(f map[string]string, name string, v *string, min, max int) {
	if v == nil {
		if min > 0 {
			f[name] = "zorunlu"
		}
		return
	}
	*v = strings.TrimSpace(*v)
	if l := len([]rune(*v)); l < min || l > max {
		f[name] = strconv.Itoa(min) + "-" + strconv.Itoa(max) + " karakter olmalı"
	}
}

func (h *Handler) createEmployee(w http.ResponseWriter, r *http.Request) error {
	var in employeeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	if in.EmployeeNo == nil || !employeeNo.MatchString(strings.TrimSpace(*in.EmployeeNo)) {
		f["employee_no"] = "3-20 karakter: büyük harf, rakam, tire"
	}
	trimmedLen(f, "first_name", in.FirstName, 1, 100)
	trimmedLen(f, "last_name", in.LastName, 1, 100)
	if in.Title == nil {
		in.Title = new(string)
	}
	trimmedLen(f, "title", in.Title, 0, 200)
	if in.OrgUnitID == nil {
		f["org_unit_id"] = "zorunlu"
	}
	if in.HireDate == nil {
		f["hire_date"] = "zorunlu"
	}
	hire := parseDate(f, "hire_date", in.HireDate)
	var salary money.Kurus
	var salaryFrom *time.Time
	if in.Salary != nil {
		var err error
		if salary, err = money.Parse(in.Salary.Amount); err != nil || salary <= 0 {
			f["salary.amount"] = "pozitif tutar, en fazla iki ondalık (ör. 45000.00)"
		}
		salaryFrom = parseDate(f, "salary.valid_from", &in.Salary.ValidFrom)
		if hire != nil && salaryFrom != nil && salaryFrom.Before(*hire) {
			f["salary.valid_from"] = "işe giriş tarihinden önce olamaz"
		}
		if !principal(r).Can(auth.PermSalaryWrite) {
			return httpx.ErrForbidden
		}
	}
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	no := strings.TrimSpace(*in.EmployeeNo)

	var id int64
	err := db.InTx(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `
			INSERT INTO employees (employee_no, first_name, last_name, title, org_unit_id, hire_date)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			no, *in.FirstName, *in.LastName, *in.Title, *in.OrgUnitID, *hire).Scan(&id)
		switch db.PgCode(err) {
		case "":
		case "23505":
			return errConflict("employee_no_taken", "bu sicil numarası kullanılıyor")
		case "23503":
			return httpx.Validation(map[string]string{"org_unit_id": "birim bulunamadı"})
		default:
			return err
		}
		newV := map[string]any{"employee_no": no, "first_name": *in.FirstName, "last_name": *in.LastName,
			"title": *in.Title, "org_unit_id": *in.OrgUnitID, "hire_date": *in.HireDate}
		if salaryFrom != nil {
			if err := h.insertSalary(r.Context(), tx, principal(r).UserID, id, *salaryFrom, salary); err != nil {
				return err
			}
			newV["salary"] = "set" // amounts never enter the audit log
		}
		return audit.Write(r.Context(), tx, auditEntry(r, "employee.create", "employee", id, nil, newV))
	})
	if err != nil {
		return err
	}
	e, err := h.loadVisible(r.Context(), principal(r), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, e)
	return nil
}

// updateEmployee changes card fields. A transfer (org_unit_id) moves the
// employee to another manager's sub-tree immediately.
func (h *Handler) updateEmployee(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in employeeInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	if in.EmployeeNo != nil || in.HireDate != nil || in.Salary != nil {
		f["_"] = "sicil no, işe giriş tarihi ve maaş bu uçtan değiştirilemez"
	}
	if in.FirstName != nil {
		trimmedLen(f, "first_name", in.FirstName, 1, 100)
	}
	if in.LastName != nil {
		trimmedLen(f, "last_name", in.LastName, 1, 100)
	}
	if in.Title != nil {
		trimmedLen(f, "title", in.Title, 0, 200)
	}
	parseDate(f, "termination_date", in.TerminationDate)
	if len(f) > 0 {
		return httpx.Validation(f)
	}

	var before Employee
	err = db.InTx(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		before, err = scanEmployee(tx.QueryRow(r.Context(), `
			SELECT `+employeeCols+` FROM employees e JOIN org_units u ON u.id = e.org_unit_id
			 WHERE e.id = $1 FOR UPDATE OF e`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), `
			UPDATE employees SET
			       first_name       = COALESCE($2, first_name),
			       last_name        = COALESCE($3, last_name),
			       title            = COALESCE($4, title),
			       org_unit_id      = COALESCE($5, org_unit_id),
			       termination_date = COALESCE($6::date, termination_date)
			 WHERE id = $1`, id, in.FirstName, in.LastName, in.Title, in.OrgUnitID, in.TerminationDate)
		switch db.PgCode(err) {
		case "":
		case "23503":
			return httpx.Validation(map[string]string{"org_unit_id": "birim bulunamadı"})
		case "23514":
			return httpx.Validation(map[string]string{"termination_date": "işe giriş tarihinden önce olamaz"})
		default:
			return err
		}
		return audit.Write(r.Context(), tx, auditEntry(r, "employee.update", "employee", id, before, in))
	})
	if err != nil {
		return err
	}
	e, err := h.loadVisible(r.Context(), principal(r), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, e)
	return nil
}

func (h *Handler) insertSalary(ctx context.Context, tx pgx.Tx, actor, employeeID int64, validFrom time.Time, amount money.Kurus) error {
	return InsertSalary(ctx, tx, h.box, &actor, employeeID, validFrom, amount)
}

// listSalaries returns the salary history to salary.read holders and to the
// employee themself.
func (h *Handler) listSalaries(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	p := principal(r)
	if _, err := h.loadVisible(r.Context(), p, id); err != nil {
		return err
	}
	if !canSeeSalary(p, id) {
		return httpx.ErrForbidden
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT valid_from, amount_enc FROM employee_salaries WHERE employee_id = $1 ORDER BY valid_from DESC`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []SalaryRow{}
	for rows.Next() {
		var from time.Time
		var enc []byte
		if err := rows.Scan(&from, &enc); err != nil {
			return err
		}
		v, err := h.box.OpenInt64(enc, salaryAAD(id, from))
		if err != nil {
			return err
		}
		items = append(items, SalaryRow{ValidFrom: from.Format(dateLayout), Amount: money.Kurus(v).String()})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (h *Handler) currentSalary(ctx context.Context, id int64) (*SalaryRow, error) {
	var from time.Time
	var enc []byte
	err := h.pool.QueryRow(ctx, `
		SELECT valid_from, amount_enc FROM employee_salaries
		 WHERE employee_id = $1 AND valid_from <= $2 ORDER BY valid_from DESC LIMIT 1`, id, h.today()).Scan(&from, &enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := h.box.OpenInt64(enc, salaryAAD(id, from))
	if err != nil {
		return nil, err
	}
	return &SalaryRow{ValidFrom: from.Format(dateLayout), Amount: money.Kurus(v).String()}, nil
}

func (h *Handler) addSalary(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in SalaryRow
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	amount, err := money.Parse(in.Amount)
	if err != nil || amount <= 0 {
		f["amount"] = "pozitif tutar, en fazla iki ondalık (ör. 45000.00)"
	}
	from := parseDate(f, "valid_from", &in.ValidFrom)
	if len(f) > 0 {
		return httpx.Validation(f)
	}
	if _, err := h.loadVisible(r.Context(), principal(r), id); err != nil {
		return err
	}
	err = db.InTx(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := h.insertSalary(r.Context(), tx, principal(r).UserID, id, *from, amount); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, auditEntry(r, "employee.salary_set", "employee", id, nil,
			map[string]string{"valid_from": in.ValidFrom}))
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, SalaryRow{ValidFrom: in.ValidFrom, Amount: amount.String()})
	return nil
}
