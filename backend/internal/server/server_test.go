package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bordroplus/internal/config"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newTestServer(db Pinger) http.Handler {
	return New(Deps{
		Config:  config.Config{RequestTimeout: 5 * time.Second},
		Log:     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		DB:      db,
		Version: "test",
	})
}

func TestHealthOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(fakeDB{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Checks["database"] != "ok" {
		t.Fatalf("unexpected body %+v", body)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID header")
	}
}

func TestHealthDatabaseDown(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(fakeDB{err: errors.New("down")}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"database":"unreachable"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestNotFoundUsesErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(fakeDB{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "not_found" || body.Error.Message == "" {
		t.Fatalf("unexpected envelope %s", rec.Body.String())
	}
}

func TestMetricsExposed(t *testing.T) {
	h := newTestServer(fakeDB{})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `http_requests_total{method="GET",route="/health",status="200"} 1`) {
		t.Fatalf("metrics missing health counter:\n%s", rec.Body.String())
	}
}
