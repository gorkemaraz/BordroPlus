package config

import (
	"strings"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", strings.Repeat("s", MinJWTSecretLen))
	t.Setenv("DEMO_PASSWORD", "Demo.12345")
	t.Setenv("SALARY_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
}

func TestLoadDefaultsAndOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("HTTP_REQUEST_TIMEOUT", "7s")
	t.Setenv("CORS_ORIGINS", "http://a, http://b ,")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.RequestTimeout != 7*time.Second {
		t.Errorf("RequestTimeout = %s", c.RequestTimeout)
	}
	if len(c.CORSOrigins) != 2 || c.CORSOrigins[1] != "http://b" {
		t.Errorf("CORSOrigins = %v", c.CORSOrigins)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("HTTP_REQUEST_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing DATABASE_URL and bad duration")
	}
}

func TestLoadAuthSettings(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.CookieSecure {
		t.Error("CookieSecure must default to true outside development")
	}
	if c.AccessTokenTTL != 15*time.Minute || c.MaxFailedLogins != 5 {
		t.Errorf("defaults: ttl=%s maxFailures=%d", c.AccessTokenTTL, c.MaxFailedLogins)
	}
}

func TestLoadRejectsWeakAuthSettings(t *testing.T) {
	cases := map[string][2]string{
		"short secret":        {"JWT_SECRET", "too-short"},
		"refresh <= access":   {"REFRESH_TOKEN_TTL", "1m"},
		"short demo password": {"DEMO_PASSWORD", "123"},
		"bad bool":            {"COOKIE_SECURE", "maybe"},
		"short salary key":    {"SALARY_ENCRYPTION_KEY", "c2hvcnQ="},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(kv[0], kv[1])
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s=%s", kv[0], kv[1])
			}
		})
	}
}
