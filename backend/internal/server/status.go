package server

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/httpx"
)

type simulatorStatus struct {
	Seed        int64      `json:"seed"`
	Employees   int        `json:"employees"`
	HistoryFrom string     `json:"history_from"`
	HistoryTo   string     `json:"history_to"`
	LoadedAt    time.Time  `json:"loaded_at"`
	LoadMS      int        `json:"load_ms"`
	Punches     int64      `json:"punches_loaded"`
	LastTickAt  *time.Time `json:"last_tick_at"`
	LivePunches int64      `json:"live_punches"`
}

// statusHandler shows that the system is alive: headcount, today's punches
// and the simulator heartbeat. Any authenticated user may read it.
func statusHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		out := map[string]any{"server_time": time.Now().UTC()}
		var active, punchesToday, pendingLeave, openProposals int
		if err := pool.QueryRow(r.Context(), `
			SELECT (SELECT count(*) FROM employees WHERE termination_date IS NULL OR termination_date >= current_date),
			       (SELECT count(*) FROM time_punches WHERE punched_at >= date_trunc('day', now())),
			       (SELECT count(*) FROM leave_requests WHERE status = 'pending'),
			       (SELECT count(*) FROM punch_corrections WHERE status = 'proposed')`).
			Scan(&active, &punchesToday, &pendingLeave, &openProposals); err != nil {
			return err
		}
		out["active_employees"] = active
		out["punches_today"] = punchesToday
		out["pending_leave_requests"] = pendingLeave
		out["open_punch_proposals"] = openProposals

		var s simulatorStatus
		err := pool.QueryRow(r.Context(), `
			SELECT seed, employees, to_char(history_from, 'YYYY-MM-DD'), to_char(history_to, 'YYYY-MM-DD'),
			       loaded_at, load_ms, punches_loaded, last_tick_at, live_punches
			  FROM simulator_state WHERE id = 1`).
			Scan(&s.Seed, &s.Employees, &s.HistoryFrom, &s.HistoryTo, &s.LoadedAt, &s.LoadMS, &s.Punches, &s.LastTickAt, &s.LivePunches)
		if err == nil {
			out["simulator"] = s
		} else {
			out["simulator"] = nil
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return nil
	})
}
