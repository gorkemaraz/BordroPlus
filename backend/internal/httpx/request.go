package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
)

// MaxBodyBytes caps JSON request bodies.
const MaxBodyBytes = 1 << 20

var ErrInvalidJSON = NewError(http.StatusBadRequest, "invalid_json", "istek gövdesi geçerli JSON değil")

// DecodeJSON reads a single JSON object into dst, rejecting unknown fields,
// trailing data and bodies over MaxBodyBytes.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return NewError(http.StatusRequestEntityTooLarge, "body_too_large", "istek gövdesi çok büyük")
		}
		return ErrInvalidJSON
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidJSON
	}
	return nil
}

// ClientIP returns the peer address of the request. Forwarding headers are
// ignored because the API port is reachable directly and they can be forged.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}

// Page is a keyset-paginated list response. NextCursor is empty on the last page.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// PageLimit parses ?limit= with a default and an upper bound.
func PageLimit(r *http.Request, def, max int) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > max {
		return 0, Validation(map[string]string{"limit": "1 ile " + strconv.Itoa(max) + " arasında olmalı"})
	}
	return n, nil
}
