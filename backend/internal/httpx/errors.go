// Package httpx holds HTTP helpers shared by all handlers: the uniform error
// envelope, JSON encoding and common middleware.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Error is an API error carrying the HTTP status and a stable machine-readable code.
// Handlers return *Error values; WriteError renders them as
// {"error":{"code":"…","message":"…"}}.
type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func NewError(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// Common errors.
var (
	ErrNotFound         = NewError(http.StatusNotFound, "not_found", "kaynak bulunamadı")
	ErrMethodNotAllowed = NewError(http.StatusMethodNotAllowed, "method_not_allowed", "bu yöntem desteklenmiyor")
	ErrUnauthorized     = NewError(http.StatusUnauthorized, "unauthorized", "kimlik doğrulama gerekli")
	ErrForbidden        = NewError(http.StatusForbidden, "forbidden", "bu işlem için yetkiniz yok")
	ErrInternal         = NewError(http.StatusInternalServerError, "internal", "beklenmeyen bir hata oluştu")
	ErrTimeout          = NewError(http.StatusServiceUnavailable, "timeout", "istek zaman aşımına uğradı")
)

// Validation builds a 422 error with per-field messages.
func Validation(fields map[string]string) *Error {
	d := make(map[string]any, len(fields))
	for k, v := range fields {
		d[k] = v
	}
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "girdi doğrulaması başarısız", Details: map[string]any{"fields": d}}
}

// WriteJSON encodes v with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError renders err in the uniform envelope. Unknown errors become 500
// and are logged; their text is never leaked to the client.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		slog.ErrorContext(r.Context(), "unhandled error", "err", err, "path", r.URL.Path)
		apiErr = ErrInternal
	}
	WriteJSON(w, apiErr.Status, map[string]*Error{"error": apiErr})
}

// HandlerFunc is an http handler that returns an error instead of writing it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle adapts a HandlerFunc to http.HandlerFunc.
func Handle(h HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}
