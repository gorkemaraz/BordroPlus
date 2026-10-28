// Package logging builds the structured JSON logger used by every binary.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// sensitiveKeys are attribute names whose values must never reach the logs.
var sensitiveKeys = map[string]struct{}{
	"password":      {},
	"password_hash": {},
	"token":         {},
	"access_token":  {},
	"refresh_token": {},
	"authorization": {},
	"cookie":        {},
	"secret":        {},
}

const redacted = "[REDACTED]"

// New returns a JSON slog.Logger writing to w. Attributes with sensitive names are redacted.
func New(w io.Writer, level string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       parseLevel(level),
		ReplaceAttr: redact,
	})
	return slog.New(h)
}

func redact(_ []string, a slog.Attr) slog.Attr {
	if _, ok := sensitiveKeys[strings.ToLower(a.Key)]; ok {
		return slog.String(a.Key, redacted)
	}
	return a
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
