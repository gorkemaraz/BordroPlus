// Command simulator loads the demo company (1.500 employees, two years of
// punches and leave) once, then keeps producing today's punches.
//
//	simulator              load if needed, then run live
//	simulator healthcheck  exit 0 once the bulk history is loaded
//
// SIM_SEED (default 42) fixes the data: the same seed gives the same data.
// SIM_EMPLOYEES (default 1500) and SIM_TICK (default 15s) tune it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"bordroplus/internal/config"
	"bordroplus/internal/db"
	"bordroplus/internal/logging"
	"bordroplus/internal/secret"
	"bordroplus/internal/sim"
	"bordroplus/internal/timesheet"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "simulator:", err)
		os.Exit(1)
	}
}

func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logging.New(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL, 4, cfg.DBConnectTimeout, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		var loaded bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM simulator_state)`).Scan(&loaded); err != nil || !loaded {
			os.Exit(1)
		}
		return nil
	}

	seed, err := envInt("SIM_SEED", 42)
	if err != nil {
		return fmt.Errorf("SIM_SEED: %w", err)
	}
	n, err := envInt("SIM_EMPLOYEES", 1500)
	if err != nil || n < 1 || n > 20000 {
		return fmt.Errorf("SIM_EMPLOYEES must be 1-20000")
	}
	tick := 15 * time.Second
	if v := os.Getenv("SIM_TICK"); v != "" {
		if tick, err = time.ParseDuration(v); err != nil || tick < time.Second {
			return fmt.Errorf("SIM_TICK: invalid duration")
		}
	}
	box, err := secret.New(cfg.SalaryKey)
	if err != nil {
		return err
	}

	st, err := sim.Load(ctx, pool, box, sim.Config{Seed: uint64(seed), Employees: n, Today: timesheet.CivilDate(time.Now())}, log)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if st.Skipped {
		log.Info("simulator: history already loaded, going live")
	} else {
		log.Info("simulator: history loaded", "employees", st.Employees, "leave_requests", st.Leaves,
			"punches", st.Punches, "duration", st.Duration.Round(time.Millisecond).String(), "seed", seed)
	}

	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		inserted, err := sim.Tick(ctx, pool, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Error("simulator: tick failed", "err", err)
		} else if inserted > 0 {
			log.Info("simulator: live punches", "inserted", inserted)
		}
		select {
		case <-ctx.Done():
			slog.Info("simulator: stopping")
			return nil
		case <-t.C:
		}
	}
}
