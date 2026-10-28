// Package seed creates the demo data every installation starts with: a small
// organisation tree and one login per role. The bulk data set (1,500
// employees, two years of history) comes from the simulator service.
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/audit"
	"bordroplus/internal/auth"
	"bordroplus/internal/db"
	"bordroplus/internal/money"
	"bordroplus/internal/org"
	"bordroplus/internal/secret"
)

// salaryFrom is when the demo salaries take effect.
var salaryFrom = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// DemoDomain is the e-mail domain of the demo accounts. ".test" is reserved
// (RFC 2606) so these addresses can never reach a real mailbox.
const DemoDomain = "bordroplus.test"

type unit struct{ code, parent, name string }

// Units in insertion order (parents first).
var demoUnits = []unit{
	{"genel_mudurluk", "", "Genel Müdürlük"},
	{"bilgi_teknolojileri", "genel_mudurluk", "Bilgi Teknolojileri"},
	{"yazilim", "bilgi_teknolojileri", "Yazılım Geliştirme"},
	{"insan_kaynaklari", "genel_mudurluk", "İnsan Kaynakları"},
	{"finans", "genel_mudurluk", "Finans"},
	{"bordro", "finans", "Bordro"},
	{"muhasebe", "finans", "Muhasebe"},
	{"ic_denetim", "genel_mudurluk", "İç Denetim"},
}

// DemoAccount is one seeded login, listed in README and TESTING.md.
type DemoAccount struct {
	Email      string
	Role       auth.Role
	EmployeeNo string
	FirstName  string
	LastName   string
	Title      string
	Unit       string
	HireDate   string
	Manages    string // org unit code this employee manages, if any
	Salary     string // monthly gross from 2026-01-01, TRY
	BirthDate  string
}

// DemoAccounts are fictional people, one per role. Hire and birth dates are
// chosen so the leave engine has something to show: 14/20/26-day bands, the
// 20-day floor for an 18-year-old (D0001's first year) and for a 50-year-old
// (D0004's fourth year).
var DemoAccounts = []DemoAccount{
	{"calisan@" + DemoDomain, auth.RoleEmployee, "D0001", "Can", "Demir", "Yazılım Geliştirici", "yazilim", "2024-03-01", "", "65000.00", "2006-09-01"},
	{"yonetici@" + DemoDomain, auth.RoleManager, "D0002", "Elif", "Kaya", "Bilgi Teknolojileri Müdürü", "bilgi_teknolojileri", "2016-09-15", "bilgi_teknolojileri", "140000.00", "1985-04-12"},
	{"ik@" + DemoDomain, auth.RoleHR, "D0003", "Selin", "Arslan", "İK Uzmanı", "insan_kaynaklari", "2019-02-11", "", "85000.00", "1990-11-30"},
	{"bordro@" + DemoDomain, auth.RolePayroll, "D0004", "Burak", "Şahin", "Bordro Uzmanı", "bordro", "2021-06-01", "", "80000.00", "1975-05-20"},
	{"muhasebe@" + DemoDomain, auth.RoleAccounting, "D0005", "Deniz", "Aydın", "Muhasebe Uzmanı", "muhasebe", "2010-01-04", "", "90000.00", "1972-08-08"},
	{"denetci@" + DemoDomain, auth.RoleAuditor, "D0006", "Ece", "Koç", "İç Denetçi", "ic_denetim", "2022-11-21", "", "95000.00", "1994-03-03"},
}

// Demo inserts whatever demo data is missing, in one transaction. Existing
// rows are left alone, so a changed demo password survives restarts.
// It returns the number of accounts created.
func Demo(ctx context.Context, pool *pgxpool.Pool, box *secret.Box, password string) (int, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	created := 0
	err = db.InTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		for _, u := range demoUnits {
			if _, err := tx.Exec(ctx, `
				INSERT INTO org_units (parent_id, code, name)
				VALUES ((SELECT id FROM org_units WHERE code = NULLIF($1, '')), $2, $3)
				ON CONFLICT (code) DO NOTHING`, u.parent, u.code, u.name); err != nil {
				return fmt.Errorf("seed unit %s: %w", u.code, err)
			}
		}

		for _, a := range DemoAccounts {
			if _, err := tx.Exec(ctx, `
				INSERT INTO employees (employee_no, first_name, last_name, title, org_unit_id, hire_date, birth_date)
				VALUES ($1, $2, $3, $4, (SELECT id FROM org_units WHERE code = $5), $6::date, $7::date)
				ON CONFLICT (employee_no) DO UPDATE SET birth_date = EXCLUDED.birth_date
				 WHERE employees.birth_date IS NULL`,
				a.EmployeeNo, a.FirstName, a.LastName, a.Title, a.Unit, a.HireDate, a.BirthDate); err != nil {
				return fmt.Errorf("seed employee %s: %w", a.EmployeeNo, err)
			}
			var empID int64
			if err := tx.QueryRow(ctx, `SELECT id FROM employees WHERE employee_no = $1`, a.EmployeeNo).Scan(&empID); err != nil {
				return err
			}
			if a.Manages != "" {
				if _, err := tx.Exec(ctx, `UPDATE org_units SET manager_id = $1 WHERE code = $2 AND manager_id IS NULL`,
					empID, a.Manages); err != nil {
					return err
				}
			}
			var hasSalary bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM employee_salaries WHERE employee_id = $1)`, empID).Scan(&hasSalary); err != nil {
				return err
			}
			if !hasSalary {
				amount, err := money.Parse(a.Salary)
				if err != nil {
					return fmt.Errorf("seed salary %s: %w", a.EmployeeNo, err)
				}
				if err := org.InsertSalary(ctx, tx, box, nil, empID, salaryFrom, amount); err != nil {
					return fmt.Errorf("seed salary %s: %w", a.EmployeeNo, err)
				}
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO users (email, password_hash, role, employee_id) VALUES ($1, $2, $3, $4)
				ON CONFLICT (email) DO NOTHING`, a.Email, hash, a.Role, empID)
			if err != nil {
				return fmt.Errorf("seed user %s: %w", a.Email, err)
			}
			created += int(tag.RowsAffected())
		}

		if created == 0 {
			return nil
		}
		return audit.Write(ctx, tx, audit.Entry{
			Action: "seed.demo_accounts", EntityType: "user",
			New: map[string]int{"accounts_created": created},
		})
	})
	return created, err
}
