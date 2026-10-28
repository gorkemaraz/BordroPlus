// Package server wires the HTTP router: middleware, system endpoints and the
// versioned API routes.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"bordroplus/internal/audit"
	"bordroplus/internal/auth"
	"bordroplus/internal/config"
	"bordroplus/internal/httpx"
	"bordroplus/internal/idempotency"
	"bordroplus/internal/leave"
	"bordroplus/internal/org"
	"bordroplus/internal/payroll"
	"bordroplus/internal/report"
	"bordroplus/internal/secret"
	"bordroplus/internal/timesheet"
)

// Pinger is the subset of the DB pool the health check needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Deps struct {
	Config  config.Config
	Log     *slog.Logger
	DB      Pinger
	Version string
	// Pool backs the /api/v1 routes. When nil only the system endpoints are
	// mounted, which lets their tests run without a database.
	Pool *pgxpool.Pool
	// Box encrypts and decrypts salaries.
	Box *secret.Box
}

// New builds the root handler.
func New(d Deps) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metrics := httpx.NewMetrics(reg)

	r := chi.NewRouter()
	r.Use(
		httpx.WithRequestID,
		httpx.Observe(d.Log, metrics),
		httpx.Recover(d.Log),
		httpx.SecurityHeaders,
		httpx.CORS(d.Config.CORSOrigins),
		httpx.Timeout(d.Config.RequestTimeout),
	)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { httpx.WriteError(w, r, httpx.ErrNotFound) })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { httpx.WriteError(w, r, httpx.ErrMethodNotAllowed) })

	r.Get("/health", healthHandler(d.DB, d.Version))
	r.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))

	if d.Pool != nil {
		r.Route("/api/v1", func(api chi.Router) { mountAPI(api, d) })
	}
	return r
}

// mountAPI wires the versioned routes. Every route except login, refresh and
// logout sits behind Authenticate and declares the permission it needs.
func mountAPI(api chi.Router, d Deps) {
	cfg := d.Config
	tokens := auth.NewTokens(cfg.JWTSecret, cfg.AccessTokenTTL)
	svc := auth.NewService(d.Pool, tokens, auth.Policy{
		RefreshTTL:      cfg.RefreshTokenTTL,
		MaxFailedLogins: cfg.MaxFailedLogins,
		LockoutDuration: cfg.LockoutDuration,
	})
	authH := auth.NewHandler(svc, tokens, cfg.RefreshTokenTTL, cfg.CookieSecure)

	api.Route("/auth", authH.Routes)

	api.Group(func(p chi.Router) {
		p.Use(authH.Authenticate)
		p.With(auth.Require(auth.PermAuditRead)).Get("/audit-log", audit.ListHandler(d.Pool))
		idem := idempotency.Middleware(d.Pool)
		org.NewHandler(d.Pool, d.Box).Routes(p, idem)
		leave.NewHandler(leave.NewService(d.Pool)).Routes(p, idem)
		timesheet.NewHandler(timesheet.NewService(d.Pool)).Routes(p, idem)
		payroll.NewHandler(payroll.NewService(d.Pool, d.Box), int(d.Config.DBMaxConns)-4, idem).Routes(p)
		report.NewHandler(d.Pool).Routes(p)
	})
}

type healthResponse struct {
	Status  string            `json:"status"`
	Version string            `json:"version"`
	Time    time.Time         `json:"time"`
	Checks  map[string]string `json:"checks"`
}

// healthHandler reports 200 only when the database answers a ping.
func healthHandler(db Pinger, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		resp := healthResponse{Status: "ok", Version: version, Time: time.Now().UTC(), Checks: map[string]string{"database": "ok"}}
		status := http.StatusOK
		if err := db.Ping(ctx); err != nil {
			resp.Status = "degraded"
			resp.Checks["database"] = "unreachable"
			status = http.StatusServiceUnavailable
		}
		httpx.WriteJSON(w, status, resp)
	}
}
