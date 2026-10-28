package org

import (
	"context"
	"strconv"
	"time"

	"bordroplus/internal/db"
	"bordroplus/internal/money"
	"bordroplus/internal/secret"
)

// SalaryAAD binds a ciphertext to its row identity, so a ciphertext copied
// to another employee or date fails to decrypt.
func SalaryAAD(employeeID int64, validFrom time.Time) []byte {
	return []byte("employee_salaries:" + strconv.FormatInt(employeeID, 10) + ":" + validFrom.Format(dateLayout))
}

// ErrSalaryExists is returned when a salary already starts on that date.
var ErrSalaryExists = errConflict("salary_exists", "bu tarihte başlayan bir maaş kaydı zaten var")

// InsertSalary encrypts and stores a monthly gross salary valid from
// validFrom. actor is nil for system writes (seed, simulator).
func InsertSalary(ctx context.Context, q db.Querier, box *secret.Box, actor *int64, employeeID int64, validFrom time.Time, amount money.Kurus) error {
	enc, err := box.SealInt64(int64(amount), SalaryAAD(employeeID, validFrom))
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO employee_salaries (employee_id, valid_from, amount_enc, key_version, created_by)
		VALUES ($1, $2, $3, $4, $5)`, employeeID, validFrom, enc, box.KeyVersion(), actor)
	if db.PgCode(err) == "23505" {
		return ErrSalaryExists
	}
	return err
}
