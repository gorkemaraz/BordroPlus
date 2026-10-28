// Package config loads all runtime configuration from environment variables.
// Nothing is read from files so the same image runs unchanged in every environment.
package config

import (
	"encoding/base64"
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

	// Authentication.
	JWTSecret       []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CookieSecure    bool
	MaxFailedLogins int
	LockoutDuration time.Duration

	// SalaryKey is the base64 AES-256 key that encrypts salaries.
	SalaryKey string

	// Demo seed (run by the migrate command).
	SeedDemo     bool
	DemoPassword string
}

// MinJWTSecretLen is the minimum HS256 key length in bytes.
const MinJWTSecretLen = 32

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

		JWTSecret:       []byte(os.Getenv("JWT_SECRET")),
		AccessTokenTTL:  dur("ACCESS_TOKEN_TTL", 15*time.Minute, &errs),
		RefreshTokenTTL: dur("REFRESH_TOKEN_TTL", 7*24*time.Hour, &errs),
		MaxFailedLogins: intVal("LOGIN_MAX_FAILURES", 5, &errs),
		LockoutDuration: dur("LOGIN_LOCKOUT", 15*time.Minute, &errs),
		SalaryKey:       os.Getenv("SALARY_ENCRYPTION_KEY"),
		SeedDemo:        boolVal("SEED_DEMO", true, &errs),
		DemoPassword:    os.Getenv("DEMO_PASSWORD"),
	}
	// Browsers accept Secure cookies on http://localhost, but plain-HTTP
	// deployments elsewhere would lose the refresh cookie; default on outside development.
	c.CookieSecure = boolVal("COOKIE_SECURE", c.Env != "development", &errs)
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.DBMaxConns < 1 {
		errs = append(errs, errors.New("DB_MAX_CONNS must be >= 1"))
	}
	if len(c.JWTSecret) < MinJWTSecretLen {
		errs = append(errs, fmt.Errorf("JWT_SECRET must be at least %d bytes", MinJWTSecretLen))
	}
	if c.AccessTokenTTL <= 0 || c.RefreshTokenTTL <= c.AccessTokenTTL {
		errs = append(errs, errors.New("token TTLs must satisfy 0 < ACCESS_TOKEN_TTL < REFRESH_TOKEN_TTL"))
	}
	if k, err := base64.StdEncoding.DecodeString(c.SalaryKey); err != nil || len(k) != 32 {
		errs = append(errs, errors.New("SALARY_ENCRYPTION_KEY must be 32 bytes, base64-encoded"))
	}
	if c.MaxFailedLogins < 1 {
		errs = append(errs, errors.New("LOGIN_MAX_FAILURES must be >= 1"))
	}
	if c.SeedDemo && len(c.DemoPassword) < 8 {
		errs = append(errs, errors.New("DEMO_PASSWORD must be at least 8 characters when SEED_DEMO is on"))
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

func boolVal(key string, def bool, errs *[]error) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return b
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
