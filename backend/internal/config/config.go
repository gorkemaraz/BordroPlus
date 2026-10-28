// Package config loads all runtime configuration from environment variables.
// Nothing is read from files so the same image runs unchanged in every environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env         string
	HTTPAddr    string
	DatabaseURL string
	LogLevel    string

	DBMaxConns        int32
	DBConnectTimeout  time.Duration
	RequestTimeout    time.Duration
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration

	CORSOrigins []string
}

// Load reads the configuration and validates required values.
func Load() (Config, error) {
	var errs []error
	c := Config{
		Env:               str("APP_ENV", "development"),
		HTTPAddr:          str("HTTP_ADDR", ":8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		LogLevel:          str("LOG_LEVEL", "info"),
		DBMaxConns:        int32(intVal("DB_MAX_CONNS", 20, &errs)),
		DBConnectTimeout:  dur("DB_CONNECT_TIMEOUT", 60*time.Second, &errs),
		RequestTimeout:    dur("HTTP_REQUEST_TIMEOUT", 30*time.Second, &errs),
		ReadHeaderTimeout: dur("HTTP_READ_HEADER_TIMEOUT", 5*time.Second, &errs),
		ShutdownTimeout:   dur("SHUTDOWN_TIMEOUT", 15*time.Second, &errs),
		CORSOrigins:       list("CORS_ORIGINS"),
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.DBMaxConns < 1 {
		errs = append(errs, errors.New("DB_MAX_CONNS must be >= 1"))
	}
	return c, errors.Join(errs...)
}

func str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func intVal(key string, def int, errs *[]error) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return n
}

func dur(key string, def time.Duration, errs *[]error) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return d
}

func list(key string) []string {
	var out []string
	for _, p := range strings.Split(os.Getenv(key), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
