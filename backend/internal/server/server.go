// Package server wires the HTTP router: middleware, system endpoints and the
// versioned API routes.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"bordroplus/internal/config"
	"bordroplus/internal/httpx"
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

	r.Route("/api/v1", func(api chi.Router) {
		// Domain routes are mounted here in later stages.
	})
	return r
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
