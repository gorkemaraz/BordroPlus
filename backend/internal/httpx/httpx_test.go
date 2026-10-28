package httpx

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriteErrorHidesUnknownErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("pq: secret internals"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret internals") {
		t.Fatal("internal error text leaked to client")
	}
	if !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestValidationError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodPost, "/", nil), Validation(map[string]string{"email": "zorunlu"}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"email":"zorunlu"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRecoverReturns500Envelope(t *testing.T) {
	h := Recover(slog.New(slog.NewJSONHandler(io.Discard, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestTimeoutSetsDeadline(t *testing.T) {
	var hasDeadline bool
	h := Timeout(time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, hasDeadline = r.Context().Deadline()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !hasDeadline {
		t.Fatal("request context has no deadline")
	}
}

func TestRequestIDReusedAndBounded(t *testing.T) {
	var seen string
	h := WithRequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = RequestID(r.Context()) }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "abc")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "abc" {
		t.Fatalf("incoming id not reused: %q", seen)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, strings.Repeat("x", 100))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(seen) != 16 {
		t.Fatalf("oversized id should be replaced, got %q", seen)
	}
}

func TestObserveDoesNotLogHeaders(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Observe(log, NewMetrics(nopRegisterer{}))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer super-secret-token")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if strings.Contains(out, "super-secret-token") {
		t.Fatal("token leaked into access log")
	}
	if !strings.Contains(out, `"status":418`) {
		t.Fatalf("status not logged: %s", out)
	}
}
