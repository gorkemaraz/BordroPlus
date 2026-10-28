// Package idempotency makes POST requests safe to retry. A client sends
// Idempotency-Key: <unique value>; the first request runs and its response is
// stored, and a retry with the same key and body gets that response again
// without running the handler a second time.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bordroplus/internal/auth"
	"bordroplus/internal/httpx"
)

const (
	Header         = "Idempotency-Key"
	ReplayedHeader = "Idempotent-Replayed"
	maxKeyLen      = 255
)

var (
	errKeyReused  = httpx.NewError(http.StatusUnprocessableEntity, "idempotency_key_reused", "bu Idempotency-Key farklı bir istekle kullanılmış")
	errInProgress = httpx.NewError(http.StatusConflict, "idempotency_in_progress", "aynı Idempotency-Key ile bir istek hâlâ işleniyor")
	errBadKey     = httpx.NewError(http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key 1-255 karakter olmalı")
)

// Middleware applies to POSTs that carry the header and must run after
// auth.Authenticate: keys are scoped per user. Requests without the header
// pass through unchanged. Keys expire after 24 hours.
//
// Server errors (5xx) are not stored, so the client can retry them. Known
// limit: the stored response is written after the handler's own transaction
// commits; a crash between the two lets one retry run again.
func Middleware(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(Header)
			if r.Method != http.MethodPost || key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > maxKeyLen {
				httpx.WriteError(w, r, errBadKey)
				return
			}
			p, ok := auth.FromContext(r.Context())
			if !ok {
				httpx.WriteError(w, r, httpx.ErrUnauthorized)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxBodyBytes))
			if err != nil {
				httpx.WriteError(w, r, httpx.NewError(http.StatusRequestEntityTooLarge, "body_too_large", "istek gövdesi çok büyük"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			h := sha256.New()
			h.Write([]byte(r.Method + " " + r.URL.Path + "\n"))
			h.Write(body)
			hash := h.Sum(nil)

			ctx := r.Context()
			if _, err := pool.Exec(ctx, `
				DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2 AND created_at < now() - interval '24 hours'`,
				p.UserID, key); err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			tag, err := pool.Exec(ctx, `
				INSERT INTO idempotency_keys (user_id, key, request_hash) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, p.UserID, key, hash)
			if err != nil {
				httpx.WriteError(w, r, err)
				return
			}
			if tag.RowsAffected() == 0 {
				replay(w, r, pool, p.UserID, key, hash)
				return
			}

			rec := &recorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			// Store with a fresh context: the request context may already be cancelled.
			storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if rec.status >= 500 || rec.status == 0 {
				_, err = pool.Exec(storeCtx, `DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2`, p.UserID, key)
			} else {
				_, err = pool.Exec(storeCtx, `
					UPDATE idempotency_keys SET status_code = $3, response_body = $4, completed_at = now()
					 WHERE user_id = $1 AND key = $2`, p.UserID, key, rec.status, rec.body.Bytes())
			}
			if err != nil {
				slog.ErrorContext(storeCtx, "idempotency store failed", "err", err, "request_id", httpx.RequestID(storeCtx))
			}
		})
	}
}

func replay(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, userID int64, key string, hash []byte) {
	var (
		storedHash []byte
		status     *int
		body       []byte
	)
	err := pool.QueryRow(r.Context(), `
		SELECT request_hash, status_code, response_body FROM idempotency_keys WHERE user_id = $1 AND key = $2`,
		userID, key).Scan(&storedHash, &status, &body)
	switch {
	case errors.Is(err, pgx.ErrNoRows): // the first attempt failed with 5xx and was removed meanwhile
		httpx.WriteError(w, r, errInProgress)
	case err != nil:
		httpx.WriteError(w, r, err)
	case !bytes.Equal(storedHash, hash):
		httpx.WriteError(w, r, errKeyReused)
	case status == nil:
		httpx.WriteError(w, r, errInProgress)
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set(ReplayedHeader, "true")
		w.WriteHeader(*status)
		_, _ = w.Write(body)
	}
}

type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}
