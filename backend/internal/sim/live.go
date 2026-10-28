package sim

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/leave"
	"bordroplus/internal/timesheet"
)

// liveCatchUp bounds how many days a restarted simulator fills in.
const liveCatchUp = 7

// Tick inserts every punch of the days after the loaded history (up to
// liveCatchUp days) whose time has come by now. Device punches are unique
// per (employee, time, direction), so repeated ticks insert each punch
// once. Employees on approved leave that day, and months whose timesheet
// is already approved, are skipped. It returns how many punches were new.
func Tick(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int, error) {
	var (
		seed     int64
		n        int
		from, to time.Time
	)
	err := pool.QueryRow(ctx, `SELECT seed, employees, history_from, history_to FROM simulator_state WHERE id = 1`).
		Scan(&seed, &n, &from, &to)
	if err != nil {
		return 0, err
	}
	today := timesheet.CivilDate(now)
	first := to.AddDate(0, 0, 1)
	if min := today.AddDate(0, 0, -liveCatchUp); first.Before(min) {
		first = min
	}
	people := People(uint64(seed), n, from, to)

	h, err := leave.LoadHolidays(ctx, pool, first, today.AddDate(0, 0, 1))
	if err != nil {
		return 0, err
	}
	ids := map[string]int64{}
	rows, err := pool.Query(ctx, `SELECT employee_no, id FROM employees WHERE employee_no LIKE 'S%' AND termination_date IS NULL`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var no string
		var id int64
		if err := rows.Scan(&no, &id); err != nil {
			rows.Close()
			return 0, err
		}
		ids[no] = id
	}
	rows.Close()

	type absentKey struct {
		emp int64
		day time.Time
	}
	absent := map[absentKey]bool{}
	rows, err = pool.Query(ctx, `
		SELECT r.employee_id, d::date FROM leave_requests r,
		       generate_series(greatest(r.start_date, $1::date), least(r.end_date, $2::date), interval '1 day') d
		 WHERE r.status = 'approved' AND r.end_date >= $1 AND r.start_date <= $2`, first, today)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var k absentKey
		if err := rows.Scan(&k.emp, &k.day); err != nil {
			rows.Close()
			return 0, err
		}
		absent[k] = true
	}
	rows.Close()
	// Approved (frozen) timesheet months never receive new punches.
	frozen := map[absentKey]bool{}
	rows, err = pool.Query(ctx, `SELECT employee_id, month FROM timesheet_periods WHERE month >= $1`, timesheet.MonthOf(first))
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var k absentKey
		if err := rows.Scan(&k.emp, &k.day); err != nil {
			rows.Close()
			return 0, err
		}
		frozen[k] = true
	}
	rows.Close()

	var emp []int64
	var when []time.Time
	var dir, dev []string
	for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
		for _, p := range people {
			id, ok := ids[p.No]
			if !ok || d.Before(p.Hire) || absent[absentKey{id, d}] || frozen[absentKey{id, timesheet.MonthOf(d)}] {
				continue
			}
			for _, e := range Day(uint64(seed), p, d, h) {
				if e.At.After(now) {
					continue
				}
				emp, when, dir, dev = append(emp, id), append(when, e.At), append(dir, e.Dir), append(dev, p.Device)
			}
		}
	}
	var inserted int
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO time_punches (employee_id, punched_at, direction, source, device_id)
			SELECT * FROM unnest($1::bigint[], $2::timestamptz[], $3::text[], array_fill('device'::text, ARRAY[cardinality($1::bigint[])]), $4::text[])
			ON CONFLICT DO NOTHING`, emp, when, dir, dev)
		if err != nil {
			return err
		}
		inserted = int(tag.RowsAffected())
		_, err = tx.Exec(ctx, `UPDATE simulator_state SET last_tick_at = now(), live_punches = live_punches + $1 WHERE id = 1`, inserted)
		return err
	})
	return inserted, err
}
