package config

import (
	"testing"
	"time"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
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
