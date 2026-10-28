package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"bordroplus/api"
	"bordroplus/internal/config"
)

// TestOpenAPIMatchesRouter keeps the published API description honest:
// every route the router serves is documented with its method, and every
// documented operation exists.
func TestOpenAPIMatchesRouter(t *testing.T) {
	var spec struct {
		OpenAPI string                               `yaml:"openapi"`
		Paths   map[string]map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatalf("openapi.yaml is not valid YAML: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") {
		t.Fatalf("openapi version %q", spec.OpenAPI)
	}
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}

	// A pool that never connects: only the route table is needed.
	pool, err := pgxpool.New(context.Background(), "postgres://x:x@127.0.0.1:1/x")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	h := New(Deps{Config: config.Config{RequestTimeout: time.Second, JWTSecret: []byte(strings.Repeat("k", 32)), DBMaxConns: 8},
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), DB: fakeDB{}, Pool: pool, Version: "test"})

	served := map[string]bool{}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete}
	err = chi.Walk(h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "/metrics" {
			method = http.MethodGet // mounted with Handle for every method
		}
		if slices.Contains(methods, method) {
			served[method+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for op := range served {
		if !documented[op] {
			t.Errorf("served but not documented: %s", op)
		}
	}
	for op := range documented {
		if !served[op] {
			t.Errorf("documented but not served: %s", op)
		}
	}
	if len(served) < 50 { // guards against an empty walk
		t.Fatalf("only %d routes found", len(served))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "BordroPlus API") {
		t.Fatalf("openapi.yaml served: %d", rec.Code)
	}
}
