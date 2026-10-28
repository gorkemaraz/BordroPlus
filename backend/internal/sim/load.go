package sim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/auth"
	"bordroplus/internal/db"
	"bordroplus/internal/leave"
	"bordroplus/internal/money"
	"bordroplus/internal/org"
	"bordroplus/internal/secret"
)

// Config controls a load.
type Config struct {
	Seed      uint64
	Employees int
	Today     time.Time // civil date the history ends before
}

// Window is the simulated history: the 24 full months before Today's month
// plus Today's month up to yesterday.
func (c Config) Window() (from, to time.Time) {
	from = time.Date(c.Today.Year(), c.Today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(-2, 0, 0)
	return from, c.Today.AddDate(0, 0, -1)
}

// Stats summarise a load.
type Stats struct {
	Employees int
	Leaves    int
	Punches   int64
	Duration  time.Duration
	Skipped   bool
}

// systemEmail is the inactive account that owns simulated records.
const systemEmail = "simulator@bordroplus.test"

// Load writes the whole simulated history in one transaction, unless a
// previous load already did (simulator_state). Interrupted loads roll back
// and are retried on the next start.
func Load(ctx context.Context, pool *pgxpool.Pool, box *secret.Box, cfg Config, log *slog.Logger) (Stats, error) {
	started := time.Now()
	var st Stats
	err := db.InTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// One loader at a time, even with two simulator containers.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7301)`); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM simulator_state)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			st.Skipped = true
			return nil
		}
		from, to := cfg.Window()
		h, err := leave.LoadHolidays(ctx, tx, from, to.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		people := People(cfg.Seed, cfg.Employees, from, to)
		st.Employees = len(people)

		unitIDs, err := ensureUnits(ctx, tx)
		if err != nil {
			return err
		}
		ids, err := insertPeople(ctx, tx, people, unitIDs)
		if err != nil {
			return err
		}
		if err := insertSalaries(ctx, tx, box, people, ids); err != nil {
			return err
		}
		if err := assignManagers(ctx, tx); err != nil {
			return err
		}
		log.Info("simulator: employees loaded", "employees", len(people))

		systemUser, err := ensureSystemUser(ctx, tx)
		if err != nil {
			return err
		}
		absences := make([][]Absence, len(people))
		for i, p := range people {
			absences[i] = Absences(cfg.Seed, p, from, to, h)
			st.Leaves += len(absences[i])
		}
		if err := insertLeaves(ctx, tx, people, ids, absences, systemUser, cfg.Today); err != nil {
			return err
		}
		log.Info("simulator: leave history loaded", "requests", st.Leaves)

		st.Punches, err = insertPunches(ctx, tx, cfg.Seed, people, ids, absences, from, to, h)
		if err != nil {
			return err
		}
		st.Duration = time.Since(started)
		_, err = tx.Exec(ctx, `
			INSERT INTO simulator_state (id, seed, employees, history_from, history_to, load_ms, punches_loaded)
			VALUES (1, $1, $2, $3, $4, $5, $6)`,
			int64(cfg.Seed), len(people), from, to, st.Duration.Milliseconds(), st.Punches)
		return err
	})
	return st, err
}

func ensureUnits(ctx context.Context, tx pgx.Tx) (map[string]int64, error) {
	for _, u := range Units {
		if u.Parent == "" {
			continue // created by the demo seed
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO org_units (parent_id, code, name) VALUES ((SELECT id FROM org_units WHERE code = $1), $2, $3)
			ON CONFLICT (code) DO NOTHING`, u.Parent, u.Code, u.Name); err != nil {
			return nil, fmt.Errorf("unit %s: %w", u.Code, err)
		}
	}
	rows, err := tx.Query(ctx, `SELECT code, id FROM org_units`)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for rows.Next() {
		var code string
		var id int64
		if err := rows.Scan(&code, &id); err != nil {
			rows.Close()
			return nil, err
		}
		out[code] = id
	}
	rows.Close()
	for _, u := range Units {
		if out[u.Code] == 0 {
			return nil, fmt.Errorf("unit %s missing (run the demo seed first)", u.Code)
		}
	}
	return out, rows.Err()
}

func insertPeople(ctx context.Context, tx pgx.Tx, people []Person, unitIDs map[string]int64) (map[string]int64, error) {
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"employees"},
		[]string{"employee_no", "first_name", "last_name", "title", "org_unit_id", "hire_date", "birth_date", "termination_date"},
		pgx.CopyFromSlice(len(people), func(i int) ([]any, error) {
			p := people[i]
			return []any{p.No, p.First, p.Last, title(p), unitIDs[p.Unit], p.Hire, p.Birth, p.Term}, nil
		}))
	if err != nil {
		return nil, fmt.Errorf("copy employees: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT employee_no, id FROM employees WHERE employee_no LIKE 'S%'`)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for rows.Next() {
		var no string
		var id int64
		if err := rows.Scan(&no, &id); err != nil {
			rows.Close()
			return nil, err
		}
		ids[no] = id
	}
	rows.Close()
	return ids, rows.Err()
}

func title(p Person) string {
	switch p.Unit {
	case "yazilim", "veri":
		return "Yazılım Mühendisi"
	case "altyapi":
		return "Sistem Uzmanı"
	case "depo", "lojistik":
		return "Operasyon Personeli"
	case "gece_vardiyasi":
		return "Gece Vardiyası Personeli"
	case "kurumsal_satis", "bayi_satis":
		return "Satış Temsilcisi"
	case "cagri_merkezi":
		return "Müşteri Temsilcisi"
	}
	return "Uzman"
}

// insertSalaries: the 2025 salary from hire (or 1 Jan 2025), the 2026 raise
// on 1 Jan 2026 (or hire).
func insertSalaries(ctx context.Context, tx pgx.Tx, box *secret.Box, people []Person, ids map[string]int64) error {
	type row struct {
		id     int64
		from   time.Time
		amount money.Kurus
	}
	var rows []row
	y25, y26 := date(2025, 1, 1), date(2026, 1, 1)
	for _, p := range people {
		id := ids[p.No]
		if p.Hire.Before(y26) {
			from := y25
			if p.Hire.After(from) {
				from = p.Hire
			}
			rows = append(rows, row{id, from, p.Salary2025})
			rows = append(rows, row{id, y26, p.Salary2026})
		} else {
			rows = append(rows, row{id, p.Hire, p.Salary2026})
		}
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"employee_salaries"}, []string{"employee_id", "valid_from", "amount_enc", "key_version"},
		pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
			r := rows[i]
			enc, err := box.SealInt64(int64(r.amount), org.SalaryAAD(r.id, r.from))
			return []any{r.id, r.from, enc, box.KeyVersion()}, err
		}))
	if err != nil {
		return fmt.Errorf("copy salaries: %w", err)
	}
	return nil
}

// assignManagers gives every unit without a manager its most senior member.
func assignManagers(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `
		UPDATE org_units u SET manager_id = (
			SELECT e.id FROM employees e WHERE e.org_unit_id = u.id AND e.termination_date IS NULL
			 ORDER BY e.hire_date, e.id LIMIT 1)
		 WHERE u.manager_id IS NULL`)
	return err
}

func ensureSystemUser(ctx context.Context, tx pgx.Tx) (int64, error) {
	var b [24]byte
	_, _ = rand.Read(b[:])
	hash, err := auth.HashPassword(hex.EncodeToString(b[:])) // nobody knows it; the account is inactive anyway
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, role, is_active) VALUES ($1, $2, 'hr', false)
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email RETURNING id`, systemEmail, hash).Scan(&id)
	return id, err
}

// insertLeaves writes approved requests and a ledger consistent with them:
// every anniversary accrual up to today, and one usage per annual leave.
func insertLeaves(ctx context.Context, tx pgx.Tx, people []Person, ids map[string]int64, absences [][]Absence, systemUser int64, today time.Time) error {
	type req struct {
		emp int64
		a   Absence
	}
	var reqs []req
	for i, p := range people {
		for _, a := range absences[i] {
			reqs = append(reqs, req{ids[p.No], a})
		}
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"leave_requests"},
		[]string{"employee_id", "leave_type", "start_date", "end_date", "half_days", "status", "requested_by", "decided_by", "decided_at", "created_at"},
		pgx.CopyFromSlice(len(reqs), func(i int) ([]any, error) {
			r := reqs[i]
			decided := time.Date(r.a.Start.Year(), r.a.Start.Month(), r.a.Start.Day(), 10, 0, 0, 0, istanbul).AddDate(0, 0, -7)
			return []any{r.emp, r.a.Type, r.a.Start, r.a.End, r.a.HalfDays, "approved", systemUser, systemUser, decided, decided}, nil
		}))
	if err != nil {
		return fmt.Errorf("copy leave requests: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id, employee_id, start_date FROM leave_requests WHERE requested_by = $1 AND leave_type = 'annual'`, systemUser)
	if err != nil {
		return err
	}
	type key struct {
		emp   int64
		start time.Time
	}
	reqIDs := map[key]int64{}
	for rows.Next() {
		var id, emp int64
		var start time.Time
		if err := rows.Scan(&id, &emp, &start); err != nil {
			rows.Close()
			return err
		}
		reqIDs[key{emp, start}] = id
	}
	rows.Close()

	type ledger struct {
		emp   int64
		kind  string
		half  int
		date  time.Time
		reqID *int64
		note  string
	}
	var entries []ledger
	balances := map[int64]int{}
	for i, p := range people {
		emp := ids[p.No]
		for _, a := range leave.Accruals(p.Hire, &p.Birth, p.Term, today) {
			entries = append(entries, ledger{emp, "accrual", a.HalfDays, a.Date, nil, fmt.Sprintf("%d. yıl hakedişi (%d gün)", a.ServiceYears, a.HalfDays/2)})
			balances[emp] += a.HalfDays
		}
		for _, a := range absences[i] {
			if a.Type != leave.TypeAnnual {
				continue
			}
			id := reqIDs[key{emp, a.Start}]
			entries = append(entries, ledger{emp, "usage", -a.HalfDays, a.Start, &id, "yıllık izin kullanımı"})
			balances[emp] -= a.HalfDays
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"leave_ledger"},
		[]string{"employee_id", "kind", "half_days", "effective_date", "leave_request_id", "note"},
		pgx.CopyFromSlice(len(entries), func(i int) ([]any, error) {
			e := entries[i]
			return []any{e.emp, e.kind, e.half, e.date, e.reqID, e.note}, nil
		})); err != nil {
		return fmt.Errorf("copy ledger: %w", err)
	}
	accounts := make([][]any, 0, len(people))
	for _, p := range people {
		emp := ids[p.No]
		if balances[emp] < 0 {
			return errors.New("simulated leave overdraws a balance")
		}
		accounts = append(accounts, []any{emp, balances[emp]})
	}
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"leave_accounts"}, []string{"employee_id", "balance_half_days"}, pgx.CopyFromRows(accounts))
	return err
}

// insertPunches streams every shift of the history into time_punches.
func insertPunches(ctx context.Context, tx pgx.Tx, seed uint64, people []Person, ids map[string]int64, absences [][]Absence,
	from, to time.Time, h leave.Holidays) (int64, error) {
	var buf []Event
	pi, d := 0, from
	var cur []Event
	next := func() bool {
		for len(cur) == 0 {
			if pi >= len(people) {
				return false
			}
			p := people[pi]
			if d.After(to) {
				pi, d = pi+1, from
				continue
			}
			day := d
			d = d.AddDate(0, 0, 1)
			if day.Before(p.Hire) || (p.Term != nil && day.After(*p.Term)) {
				continue
			}
			absent := false
			for _, a := range absences[pi] {
				absent = absent || a.covers(day)
			}
			if absent {
				continue
			}
			cur = append(buf[:0], Day(seed, p, day, h)...)
		}
		return true
	}
	return tx.CopyFrom(ctx, pgx.Identifier{"time_punches"},
		[]string{"employee_id", "punched_at", "direction", "source", "device_id"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if !next() {
				return nil, nil
			}
			e := cur[0]
			cur = cur[1:]
			p := people[pi]
			return []any{ids[p.No], e.At, e.Dir, "device", p.Device}, nil
		}))
}
